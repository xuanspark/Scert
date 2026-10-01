package deployment

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestVerifiedReloadFailureRestoresRuntimeBeforeUnlock(t *testing.T) {
	for _, failure := range []string{"exit_status", "transport_error"} {
		t.Run(failure, func(t *testing.T) {
			servers := []Server{testServer("first").Normalize(), testServer("second").Normalize()}
			clients := map[string]*memoryClient{}
			originals := map[string]map[string]memoryFile{}
			for _, server := range servers {
				client := newMemoryClient()
				clients[server.ID] = client
				originals[server.ID] = seedVerifiedFiles(client, server)
			}
			if failure == "exit_status" {
				clients["second"].runResults = []CommandResult{{}, {ExitCode: 1}}
			} else {
				clients["second"].runErrors = []error{nil, errors.New("reload response lost")}
			}
			journal := &memoryTransactionJournal{}
			service := NewServiceWithJournal(memoryFactory{clients: clients}, journal)
			ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
			recoveryCalled := false
			report, err := service.DeployVerified(ctx, []Target{{Server: servers[0]}, {Server: servers[1]}}, testArtifact(t), Verification{
				Verify: func(context.Context) error { t.Fatal("failed reload must not verify deployment"); return nil },
				VerifyRecovery: func(ctx context.Context) error {
					recoveryCalled = true
					if ctx.Err() != nil {
						t.Fatal("recovery context is cancelled")
					}
					for _, server := range servers {
						client := clients[server.ID]
						assertVerifiedFiles(t, client, originals[server.ID])
						assertVerifiedLocks(t, client, server, true)
						commands := [][]string{server.ValidationCommand, server.ReloadCommand, server.ValidationCommand, server.ReloadCommand}
						if !reflect.DeepEqual(client.commands, commands) {
							t.Fatalf("old runtime was not reloaded: %v", client.commands)
						}
					}
					return nil
				},
			})
			if err == nil || report.Status != "failed" || report.RecoveryStatus != "success" || !recoveryCalled || report.Success != 0 || report.Failed != 2 {
				t.Fatalf("unexpected recovery report: %+v, %v", report, err)
			}
			if len(journal.finished) != 2 {
				t.Fatalf("unexpected journal: %+v", journal.finished)
			}
			for _, record := range journal.finished {
				if record.status != TransactionStatusRolledBack {
					t.Fatalf("unexpected transaction: %+v", record)
				}
			}
			for index, server := range servers {
				assertVerifiedLocks(t, clients[server.ID], server, false)
				if report.Results[index].FailureCode != FailureRecoveryRestored {
					t.Fatalf("unexpected result: %+v", report.Results[index])
				}
			}
		})
	}
}

func TestVerifiedReloadFailureKeepsLocksWithoutRecoveryProof(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		recovery     func(context.Context) error
		restoreExit  int
		wantRecovery string
	}{
		{"missing_verifier", nil, 0, "unconfirmed"},
		{"wrong_runtime", func(context.Context) error { return errors.New("old fingerprint not served") }, 0, "unconfirmed"},
		{"restore_reload_failed", func(context.Context) error { t.Fatal("failed restore must not verify"); return nil }, 1, "failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client, server := newMemoryClient(), testServer("failed-reload")
			original := seedVerifiedFiles(client, server)
			client.runResults = []CommandResult{{}, {ExitCode: 1}, {}, {ExitCode: scenario.restoreExit}}
			journal := &memoryTransactionJournal{}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
			report, err := service.DeployVerified(ctx, []Target{{Server: server}}, testArtifact(t), Verification{
				Verify:         func(context.Context) error { t.Fatal("failed reload must not verify deployment"); return nil },
				VerifyRecovery: scenario.recovery,
			})
			if err == nil || report.Status != "failed" || report.RecoveryStatus != scenario.wantRecovery {
				t.Fatalf("unexpected report: %+v, %v", report, err)
			}
			assertVerifiedFiles(t, client, original)
			assertVerifiedLocks(t, client, server, true)
			if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRecoveryRequired {
				t.Fatalf("unexpected journal: %+v", journal.finished)
			}
		})
	}
}
