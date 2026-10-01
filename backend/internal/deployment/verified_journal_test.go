package deployment

import (
	"context"
	"errors"
	"testing"
)

type failedUnlockClient struct{ *memoryClient }

func (c *failedUnlockClient) Unlock(context.Context, string) error {
	return errors.New("unlock failed")
}

func TestVerifiedRollbackJournalRetainsFailedFinalization(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		configure func(*memoryClient) Client
		wantCode  FailureCode
	}{
		{"unlock", func(c *memoryClient) Client { return &failedUnlockClient{c} }, FailureLockRelease},
		{"restore", func(c *memoryClient) Client { c.restoreError = true; return c }, FailureRollback},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client, server := newMemoryClient(), testServer("failed-finalization")
			seedVerifiedFiles(client, server)
			journal := &memoryTransactionJournal{}
			service := NewServiceWithJournal(fixedClientFactory{scenario.configure(client)}, journal)
			ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
			report, err := service.DeployVerified(ctx, []Target{{Server: server}}, testArtifact(t), Verification{
				Verify:         func(context.Context) error { return errors.New("verification failed") },
				VerifyRecovery: func(context.Context) error { return nil },
			})
			if err == nil || report.RecoveryStatus != "failed" || report.Results[0].FailureCode != scenario.wantCode {
				t.Fatalf("unexpected failure report: %+v, %v", report, err)
			}
			if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRecoveryRequired {
				t.Fatalf("failed finalization must retain recovery state: %+v", journal.finished)
			}
			assertVerifiedLocks(t, client, server, true)
		})
	}
}

func TestVerifiedPartialFailurePersistsRolledBackTarget(t *testing.T) {
	client, first := newMemoryClient(), testServer("first")
	second := testServer("second")
	original := seedVerifiedFiles(client, first)
	journal := &memoryTransactionJournal{}
	service := NewServiceWithJournal(memoryFactory{
		clients: map[string]*memoryClient{first.ID: client},
		errors:  map[string]error{second.ID: errors.New("connection failed")},
	}, journal)
	ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
	report, err := service.DeployVerified(ctx, []Target{{Server: first}, {Server: second}}, testArtifact(t), Verification{
		Verify:         func(context.Context) error { t.Fatal("partial preparation must not verify deployment"); return nil },
		VerifyRecovery: func(context.Context) error { assertVerifiedFiles(t, client, original); return nil },
	})
	if err == nil || report.Status != "failed" || report.RecoveryStatus != "success" {
		t.Fatalf("unexpected partial failure report: %+v, %v", report, err)
	}
	if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatalf("restored target must be finalized as rolled back: %+v", journal.finished)
	}
	assertVerifiedLocks(t, client, first, false)
}

func TestVerifiedRollbackJournalMatchesRecoveryEvidence(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		recoveryError error
		wantStatus    string
		wantRecovery  string
		wantLocks     bool
	}{
		{"verified", nil, TransactionStatusRolledBack, "success", false},
		{"unverified", errors.New("recovery verification failed"), TransactionStatusRecoveryRequired, "unconfirmed", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			servers := []Server{testServer("first"), testServer("second")}
			clients := map[string]*memoryClient{}
			originals := map[string]map[string]memoryFile{}
			for _, server := range servers {
				clients[server.ID] = newMemoryClient()
				originals[server.ID] = seedVerifiedFiles(clients[server.ID], server)
			}
			journal := &memoryTransactionJournal{}
			service := NewServiceWithJournal(memoryFactory{clients: clients}, journal)
			ctx := WithTransactionContext(t.Context(), TransactionContext{
				TaskID: "task", AttemptID: "attempt", AttemptNumber: 1,
			})
			verificationError := errors.New("deployment verification failed")
			report, err := service.DeployVerified(ctx, []Target{{Server: servers[0]}, {Server: servers[1]}}, testArtifact(t), Verification{
				Verify: func(context.Context) error { return verificationError },
				VerifyRecovery: func(context.Context) error {
					for _, server := range servers {
						assertVerifiedFiles(t, clients[server.ID], originals[server.ID])
						assertVerifiedLocks(t, clients[server.ID], server, true)
					}
					return scenario.recoveryError
				},
			})
			if !errors.Is(err, verificationError) || report.Status != "failed" || report.RecoveryStatus != scenario.wantRecovery {
				t.Fatalf("unexpected report: %+v, error: %v", report, err)
			}
			if len(journal.finished) != len(servers) {
				t.Fatalf("unexpected journal completion count: %d", len(journal.finished))
			}
			for _, record := range journal.finished {
				if record.status != scenario.wantStatus {
					t.Errorf("transaction %s: got %s, want %s", record.id, record.status, scenario.wantStatus)
				}
				if record.status == TransactionStatusRolledBack && record.message != publicFailureMessages[FailureRecoveryRestored] {
					t.Errorf("rolled back transaction has stale message: %s", record.message)
				}
			}
			for _, server := range servers {
				assertVerifiedLocks(t, clients[server.ID], server, scenario.wantLocks)
			}
		})
	}
}
