package deployment

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type operationWitnessContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *operationWitnessContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type operationFactory struct {
	client Client
	calls  atomic.Int32
}

func (f *operationFactory) Connect(context.Context, Server) (Client, error) {
	f.calls.Add(1)
	return f.client, nil
}

type operationCloseClient struct {
	*memoryClient
	closeHook func()
}

func (c *operationCloseClient) Close() error {
	if c.closeHook != nil {
		c.closeHook()
	}
	return nil
}

type operationJournal struct {
	memoryTransactionJournal
	commitHook func()
}

func (j *operationJournal) UpdateDeploymentTransaction(ctx context.Context, item TransactionRecord) error {
	if item.Phase == TransactionPhaseCommitting && j.commitHook != nil {
		j.commitHook()
	}
	return j.memoryTransactionJournal.UpdateDeploymentTransaction(ctx, item)
}

func awaitOperationSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach expected stage")
	}
}

func TestOperationGateCoversVerifiedLifecycle(t *testing.T) {
	for _, stage := range []string{"verify", "commit", "restore", "close"} {
		t.Run(stage, func(t *testing.T) {
			entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			release := func() { unblock.Do(func() { close(resume) }) }
			defer release()
			var hookOnce sync.Once
			hook := func() { hookOnce.Do(func() { close(entered); <-resume }) }
			server, memory := testServer("gate"), newMemoryClient()
			seedVerifiedFiles(memory, server)
			client := &operationCloseClient{memoryClient: memory}
			journal := &operationJournal{}
			if stage == "close" {
				client.closeHook = hook
			}
			if stage == "commit" {
				journal.commitHook = hook
			}
			factory := &operationFactory{client: client}
			service := NewServiceWithJournal(factory, journal)
			ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
			artifact := testArtifact(t)
			var report Report
			var runErr error
			go func() {
				defer close(done)
				report, runErr = service.DeployVerified(ctx, []Target{{Server: server}}, artifact, Verification{
					Verify: func(context.Context) error {
						if stage == "verify" {
							hook()
						}
						if stage == "restore" {
							return errors.New("injected verification failure")
						}
						return nil
					},
					VerifyRecovery: func(context.Context) error { hook(); return nil },
				})
			}()
			awaitOperationSignal(t, entered)
			calls := factory.calls.Load()
			for _, operation := range []string{"deploy", "verified", "recover"} {
				waitCtx, cancel := context.WithCancel(t.Context())
				witness := &operationWitnessContext{Context: waitCtx, waiting: make(chan struct{})}
				otherDone := make(chan struct{})
				var otherErr error
				var otherReport Report
				go func() {
					defer close(otherDone)
					if operation == "recover" {
						otherErr = service.RecoverPending(witness, func(context.Context, string) (Server, error) { return server, nil })
					} else if operation == "verified" {
						otherReport, otherErr = service.DeployVerified(witness, []Target{{Server: server}}, artifact, Verification{Verify: func(context.Context) error { return nil }})
					} else {
						otherReport = service.Deploy(witness, []Target{{Server: server}}, artifact)
					}
				}()
				awaitOperationSignal(t, witness.waiting)
				cancel()
				awaitOperationSignal(t, otherDone)
				if factory.calls.Load() != calls || (operation != "deploy" && !errors.Is(otherErr, context.Canceled)) || (operation != "recover" && otherReport.Status != "failed") {
					t.Fatalf("concurrent %s entered during %s: %+v %v", operation, stage, otherReport, otherErr)
				}
			}
			release()
			awaitOperationSignal(t, done)
			if stage == "restore" {
				if runErr == nil || report.RecoveryStatus != "success" {
					t.Fatalf("restore failed: %+v %v", report, runErr)
				}
			} else if runErr != nil || report.Status != "success" {
				t.Fatalf("deployment failed: %+v %v", report, runErr)
			}
			unlock, err := service.acquireOperation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			unlock()
		})
	}
}

type claimedOperationJournal struct {
	memoryTransactionJournal
	claimed  bool
	claimErr error
	listErr  error
}

func (j *claimedOperationJournal) HasRecoveryClaim(context.Context, string) (bool, error) {
	return j.claimed, j.claimErr
}
func (j *claimedOperationJournal) ListPendingDeploymentTransactions(ctx context.Context) ([]TransactionRecord, error) {
	if j.listErr != nil {
		return nil, j.listErr
	}
	return j.memoryTransactionJournal.ListPendingDeploymentTransactions(ctx)
}

func TestRecoveryClaimPreventsAutomaticMutation(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "claimed", true: "read_failure"}[failure], func(t *testing.T) {
			server, client, record := recoveryPreflightFixture()
			original := maps.Clone(client.files)
			journal := &claimedOperationJournal{memoryTransactionJournal: memoryTransactionJournal{pending: []TransactionRecord{record}}, claimed: true}
			want := ErrRecoveryDispositionBusy
			if failure {
				journal.claimErr = errors.New("private database error")
				want = ErrRecoveryDispositionUnavailable
			}
			factory := &operationFactory{client: client}
			service := NewServiceWithJournal(factory, journal)
			err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { t.Error("claimed record resolved"); return server, nil })
			if !errors.Is(err, want) || factory.calls.Load() != 0 || len(journal.updated) != 0 || len(journal.finished) != 0 || !reflect.DeepEqual(original, client.files) {
				t.Fatalf("claimed recovery changed state: %v", err)
			}
		})
	}
}

func TestRecoveryClaimPreventsDeploymentBeforeConnection(t *testing.T) {
	for _, scenario := range []string{"same_server", "alias", "claim_failure", "list_failure"} {
		t.Run(scenario, func(t *testing.T) {
			server, client, record := recoveryPreflightFixture()
			journal := &claimedOperationJournal{memoryTransactionJournal: memoryTransactionJournal{pending: []TransactionRecord{record}}, claimed: true}
			if scenario == "alias" {
				server.ID = "alias"
			}
			if scenario == "claim_failure" {
				journal.claimErr = errors.New("private claim error")
			}
			if scenario == "list_failure" {
				journal.listErr = errors.New("private list error")
			}
			factory := &operationFactory{client: client}
			service := NewServiceWithJournal(factory, journal)
			report := service.Deploy(t.Context(), []Target{{Server: server}}, testArtifact(t))
			if report.Status != "failed" || report.Failed != 1 || factory.calls.Load() != 0 || len(journal.pending) != 1 {
				t.Fatalf("claimed deployment entered remote: %+v", report)
			}
		})
	}
}

func TestUnclaimedRecoveryStillRuns(t *testing.T) {
	server, client, record := recoveryPreflightFixture()
	journal := &claimedOperationJournal{memoryTransactionJournal: memoryTransactionJournal{pending: []TransactionRecord{record}}}
	factory := &operationFactory{client: client}
	service := NewServiceWithJournal(factory, journal)
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
	if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err != nil {
		t.Fatal(err)
	}
	if factory.calls.Load() != 1 || len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatal("unclaimed recovery was suppressed")
	}
}

func TestCanceledOperationDoesNotLeakGate(t *testing.T) {
	service := NewService(nil)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.acquireOperation(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for range 3 {
		ctx, stop := context.WithTimeout(t.Context(), time.Second)
		unlock, err := service.acquireOperation(ctx)
		stop()
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
}
