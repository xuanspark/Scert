package deployment

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"
)

type executorJournal struct {
	memoryTransactionJournal
	item                           RecoveryDisposition
	claimed                        bool
	beginCalls, finishCalls        int
	beginErr, finishErr, updateErr error
	finishContextErr               error
	existing                       bool
	beforeFinish                   func()
}

func (j *executorJournal) BeginRecoveryDisposition(_ context.Context, intent RecoveryDispositionIntent) (RecoveryDisposition, bool, error) {
	j.beginCalls++
	if j.beginErr != nil {
		return RecoveryDisposition{}, false, j.beginErr
	}
	if j.existing {
		return j.item, false, nil
	}
	if j.claimed {
		return RecoveryDisposition{}, false, ErrRecoveryDispositionBusy
	}
	if !intent.MatchesRecord(j.pending[0]) {
		return RecoveryDisposition{}, false, ErrRecoveryReviewChanged
	}
	j.item = RecoveryDisposition{Intent: intent, Status: "running"}
	j.claimed = true
	return j.item, true, nil
}

func (j *executorJournal) FinishRecoveryDisposition(ctx context.Context, id, result string) (RecoveryDisposition, error) {
	j.finishCalls++
	j.finishContextErr = ctx.Err()
	if j.beforeFinish != nil {
		j.beforeFinish()
	}
	if j.finishErr != nil {
		return RecoveryDisposition{}, j.finishErr
	}
	if id != j.item.Intent.ID || !j.item.Intent.AllowsResult(result) || !j.claimed {
		return RecoveryDisposition{}, ErrRecoveryDispositionConflict
	}
	j.item.Status, j.item.ResultCode = "succeeded", result
	if result == RecoveryResultRejected || result == RecoveryResultUncertain {
		j.item.Status = result
	}
	j.claimed = result == RecoveryResultUncertain
	return j.item, nil
}

func (j *executorJournal) GetRecoveryDisposition(context.Context, string) (RecoveryDisposition, error) {
	return j.item, nil
}

func (j *executorJournal) HasRecoveryClaim(context.Context, string) (bool, error) {
	return j.claimed, nil
}

func (j *executorJournal) UpdateDeploymentTransaction(ctx context.Context, record TransactionRecord) error {
	if j.updateErr != nil {
		return j.updateErr
	}
	return j.memoryTransactionJournal.UpdateDeploymentTransaction(ctx, record)
}

type executorClient struct {
	*memoryClient
	closeErr    error
	closed      int
	beforeClose func()
}

func (c *executorClient) Close() error {
	c.closed++
	if c.beforeClose != nil {
		c.beforeClose()
	}
	return c.closeErr
}

func executorFixture(t *testing.T) (*Service, *executorJournal, *executorClient, Server, TransactionRecord) {
	t.Helper()
	server, memory, record := recoveryPreflightFixture()
	journal := &executorJournal{memoryTransactionJournal: memoryTransactionJournal{pending: []TransactionRecord{record}}}
	client := &executorClient{memoryClient: memory}
	service := NewServiceWithJournal(fixedClientFactory{client}, journal)
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
	return service, journal, client, server, record
}

func TestRecoveryExecutorThreeExplicitActions(t *testing.T) {
	for _, action := range []RecoveryAction{RecoveryActionRecord, RecoveryActionCommit, RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			service, journal, client, server, record := executorFixture(t)
			before := maps.Clone(client.files)
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
			if err != nil {
				t.Fatal(err)
			}
			var verified []bool
			service.VerifyRecoveryTarget = func(_ context.Context, _ Server, _ RecoveryBaseline, commit bool) error {
				verified = append(verified, commit)
				return nil
			}
			item, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
			if err != nil || item.Status != "succeeded" || journal.claimed || journal.beginCalls != 1 || journal.finishCalls != 1 || len(journal.finished) != 0 {
				t.Fatalf("execution failed: %+v, %v", item, err)
			}
			if action == RecoveryActionRecord {
				if item.ResultCode != RecoveryResultObserved || len(verified) != 0 || len(journal.updated) != 0 || !reflect.DeepEqual(before, client.files) {
					t.Fatal("record-only action changed original state")
				}
			} else {
				expected, phase, result := "new", TransactionPhaseCommitVerified, RecoveryResultCommitted
				if action == RecoveryActionRollback {
					expected, phase, result = "old", TransactionPhaseRollbackVerified, RecoveryResultRolledBack
				}
				if !reflect.DeepEqual(verified, []bool{action == RecoveryActionCommit}) || item.ResultCode != result || len(journal.updated) != 1 || journal.updated[0].Phase != phase {
					t.Fatal("wrong action, verification or phase")
				}
				for _, path := range record.Paths {
					if string(client.files[path].data) != expected {
						t.Fatal("wrong certificate files")
					}
				}
				if len(client.files) != 3 {
					t.Fatal("transaction leftovers remain")
				}
			}
			if _, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve); !errors.Is(err, ErrRecoveryConfirmation) || journal.beginCalls != 1 {
				t.Fatal("execution replayed")
			}
		})
	}
}

func TestRecoveryExecutorFailureBoundaries(t *testing.T) {
	for _, scenario := range []string{"changed", "foreign-lock", "legacy", "phase", "missing-baseline", "missing-backup-entry", "begin", "finish", "persist", "cleanup", "reload", "commit-https", "rollback-https", "cancel", "close", "existing"} {
		t.Run(scenario, func(t *testing.T) {
			service, journal, client, server, record := executorFixture(t)
			action := RecoveryActionCommit
			if scenario == "reload" || scenario == "rollback-https" || scenario == "missing-backup-entry" {
				action = RecoveryActionRollback
			}
			if scenario == "legacy" {
				record.Evidence = nil
				action = RecoveryActionRecord
			}
			if scenario == "phase" {
				record.Phase = TransactionPhaseUploaded
			}
			if scenario == "missing-baseline" {
				record.Evidence.Baseline.ExpectedFingerprint = ""
			}
			if scenario == "missing-backup-entry" {
				record.Backups = record.Backups[:2]
			}
			journal.pending[0] = record
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			verifyCalls := 0
			service.VerifyRecoveryTarget = func(_ context.Context, _ Server, _ RecoveryBaseline, commit bool) error {
				verifyCalls++
				if commit != (action == RecoveryActionCommit) {
					t.Fatal("failed action silently switched direction")
				}
				if scenario == "commit-https" || scenario == "rollback-https" {
					return errors.New("private upstream details")
				}
				if scenario == "cancel" {
					cancel()
				}
				if scenario == "close" {
					client.closeErr = errors.New("private close details")
				}
				return nil
			}
			switch scenario {
			case "changed":
				client.files[record.Paths[0]] = memoryFile{data: []byte("foreign")}
			case "foreign-lock":
				client.files[record.LockPaths[0]+"/owner"] = memoryFile{data: []byte("foreign")}
			case "begin":
				journal.beginErr = errors.New("private database error")
			case "finish":
				journal.finishErr = errors.New("private audit error")
			case "persist":
				journal.updateErr = errors.New("private update error")
			case "cleanup":
				client.removeError = ".scert-backup-"
			case "reload":
				client.runErrors = []error{errors.New("private command error")}
			case "existing":
				journal.existing = true
				journal.item = RecoveryDisposition{Status: "uncertain", ResultCode: RecoveryResultUncertain}
			}
			before := maps.Clone(client.files)
			closedBefore := client.closed
			item, err := service.ExecuteRecoveryDisposition(ctx, confirmation, "unix:0", token, resolve)
			if scenario == "legacy" {
				if err != nil || item.ResultCode != RecoveryResultObserved || len(journal.updated) != 0 || !reflect.DeepEqual(before, client.files) {
					t.Fatal("legacy inspection mutated history")
				}
				return
			}
			if scenario == "existing" {
				if err != nil || item.Status != "uncertain" || verifyCalls != 0 || journal.finishCalls != 0 || client.closed != closedBefore || !reflect.DeepEqual(before, client.files) {
					t.Fatal("existing request executed again")
				}
				return
			}
			if err == nil || item.Status == "succeeded" || journal.finishContextErr != nil {
				t.Fatalf("failure reported success or skipped independent audit: %+v %v", item, err)
			}
			uncertain := scenario == "finish" || scenario == "persist" || scenario == "cleanup" || scenario == "reload" || scenario == "rollback-https" || scenario == "close"
			if journal.claimed != uncertain {
				t.Fatalf("wrong claim retention: %v", journal.claimed)
			}
			if !uncertain && !reflect.DeepEqual(before, client.files) {
				t.Fatal("preflight rejection changed remote files")
			}
			if len(journal.finished) != 0 {
				t.Fatal("executor bypassed atomic disposition completion")
			}
		})
	}
}

func TestRecoveryExecutorWaitCancellationConsumesConfirmation(t *testing.T) {
	service, journal, _, server, record := executorFixture(t)
	resolve := func(context.Context, string) (Server, error) { return server, nil }
	confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", RecoveryActionRecord, resolve)
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.acquireOperation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := service.ExecuteRecoveryDisposition(ctx, confirmation, "unix:0", token, resolve); !errors.Is(err, context.DeadlineExceeded) || journal.beginCalls != 0 {
		t.Fatal("cancelled waiter acquired persistent claim")
	}
	if _, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve); !errors.Is(err, ErrRecoveryConfirmation) {
		t.Fatal("cancelled confirmation replayed")
	}
}

func TestRecoveryExecutorHoldsOperationThroughVerificationCloseAndAudit(t *testing.T) {
	service, journal, client, server, record := executorFixture(t)
	resolve := func(context.Context, string) (Server, error) { return server, nil }
	confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", RecoveryActionCommit, resolve)
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]int{}
	checkHeld := func(stage string) {
		checked[stage]++
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		release, err := service.acquireOperation(ctx)
		if err == nil {
			release()
			t.Fatalf("operation gate released during %s", stage)
		}
		if !errors.Is(err, context.DeadlineExceeded) || !journal.claimed {
			t.Fatalf("missing durable claim or invalid wait during %s", stage)
		}
	}
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error {
		checkHeld("verify")
		return nil
	}
	client.beforeClose = func() { checkHeld("close") }
	journal.beforeFinish = func() { checkHeld("audit") }
	if _, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve); err != nil {
		t.Fatal(err)
	}
	if checked["verify"] != 1 || checked["close"] != 2 || checked["audit"] != 1 {
		t.Fatalf("missing gate coverage: %v", checked)
	}
	release, err := service.acquireOperation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestRecoveryExecutorVerifiedHistoryStillRechecksHTTPS(t *testing.T) {
	for _, action := range []RecoveryAction{RecoveryActionCommit, RecoveryActionRollback} {
		for _, failVerification := range []bool{false, true} {
			t.Run(string(action)+map[bool]string{false: "/success", true: "/failure"}[failVerification], func(t *testing.T) {
				service, journal, client, server, record := executorFixture(t)
				record.Phase = TransactionPhaseCommitVerified
				if action == RecoveryActionRollback {
					record.Phase = TransactionPhaseRollbackVerified
					for _, path := range record.Paths {
						client.files[path] = memoryFile{data: []byte("old")}
					}
				}
				for _, path := range record.TemporaryPaths {
					delete(client.files, path)
				}
				for _, backup := range record.Backups {
					delete(client.files, backup.Saved)
				}
				delete(client.files, record.LockPaths[0])
				delete(client.files, record.LockPaths[0]+"/owner")
				journal.pending[0] = record
				resolve := func(context.Context, string) (Server, error) { return server, nil }
				confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
				if err != nil {
					t.Fatal(err)
				}
				verified := 0
				service.VerifyRecoveryTarget = func(_ context.Context, _ Server, _ RecoveryBaseline, commit bool) error {
					verified++
					if commit != (action == RecoveryActionCommit) {
						t.Fatal("wrong HTTPS verification direction")
					}
					if failVerification {
						return errors.New("HTTPS mismatch")
					}
					return nil
				}
				item, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
				if verified != 1 || (err != nil) != failVerification || (item.Status == "succeeded") == failVerification {
					t.Fatalf("historical phase bypassed new verification: %+v %v", item, err)
				}
				if !failVerification && len(client.files) != 3 {
					t.Fatal("partial unlock history failed to finish safely")
				}
				if failVerification {
					if _, exists := client.files[record.LockPaths[1]]; !exists {
						t.Fatal("failed verification removed retained lock")
					}
				}
			})
		}
	}
}

func TestRecoveryExecutorRechecksAfterHTTPSAndAuditsCancellation(t *testing.T) {
	for _, scenario := range []string{"target", "backup", "lock", "cancel-after-write"} {
		t.Run(scenario, func(t *testing.T) {
			service, journal, client, server, record := executorFixture(t)
			action := RecoveryActionCommit
			if scenario == "cancel-after-write" {
				action = RecoveryActionRollback
			}
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error {
				path := record.Paths[0]
				switch scenario {
				case "backup":
					path = record.Backups[0].Saved
				case "lock":
					path = record.LockPaths[0] + "/owner"
				case "cancel-after-write":
					cancel()
					return ctx.Err()
				}
				client.files[path] = memoryFile{data: []byte("foreign-change")}
				return nil
			}
			item, err := service.ExecuteRecoveryDisposition(ctx, confirmation, "unix:0", token, resolve)
			if err == nil || item.Status == "succeeded" || journal.finishCalls != 1 || journal.finishContextErr != nil {
				t.Fatal("changed or cancelled execution skipped audit or reported success")
			}
			if journal.claimed != (scenario == "cancel-after-write") {
				t.Fatal("wrong claim retention after cancelled write")
			}
			if _, exists := client.files[record.LockPaths[0]]; !exists {
				t.Fatal("changed state unlocked")
			}
		})
	}
}

func TestRecoveryExecutorRejectsUnlistedTemporaryFiles(t *testing.T) {
	for _, action := range []RecoveryAction{RecoveryActionCommit, RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			service, journal, client, server, record := executorFixture(t)
			delete(record.Evidence.TemporaryFiles, record.TemporaryPaths[0])
			record.TemporaryPaths = record.TemporaryPaths[1:]
			journal.pending[0] = record
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
			if err != nil {
				t.Fatal(err)
			}
			before := maps.Clone(client.files)
			service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error {
				t.Fatal("unknown temporary file reached HTTPS verification")
				return nil
			}
			item, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
			if !errors.Is(err, ErrRecoveryActionRejected) || item.ResultCode != RecoveryResultRejected || journal.claimed || !reflect.DeepEqual(before, client.files) {
				t.Fatal("unlisted temporary file ignored or modified")
			}
		})
	}
}
