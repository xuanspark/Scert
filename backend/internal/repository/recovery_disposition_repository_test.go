package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func recoveryDispositionFixture(t *testing.T, action deployment.RecoveryAction) (*SQLDeploymentTransactionRepository, *sql.DB, deployment.RecoveryDispositionIntent) {
	t.Helper()
	_, db := newTaskRepositoryTest(t)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSQLDeploymentTransactionRepository(db)
	return seedRecoveryDisposition(t, repo, db, action)
}

func seedRecoveryDisposition(t *testing.T, repo *SQLDeploymentTransactionRepository, db *sql.DB, action deployment.RecoveryAction) (*SQLDeploymentTransactionRepository, *sql.DB, deployment.RecoveryDispositionIntent) {
	t.Helper()
	record := deployment.TransactionRecord{
		ID: "recovery-transaction", TaskID: "recovery-task", AttemptID: "recovery-attempt", AttemptNumber: 1,
		ServerID: "recovery-server", ServerName: "server", Paths: []string{"/cert.pem", "/chain.pem", "/key.pem"},
		Phase: deployment.TransactionPhaseInstalled, Status: deployment.TransactionStatusRecoveryRequired,
	}
	if action != deployment.RecoveryActionRecord {
		record.Evidence = &deployment.RecoveryEvidence{Version: 1, LockToken: "fixture-secret-not-for-disposition"}
	}
	if err := repo.CreateDeploymentTransaction(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.ListPendingDeploymentTransactions(t.Context())
	if err != nil || len(stored) != 1 {
		t.Fatal("missing fixture transaction", err)
	}
	encoded, err := json.Marshal(stored[0])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	intent := deployment.RecoveryDispositionIntent{
		ID: strings.Repeat("a", 64), TransactionID: record.ID, ServerID: record.ServerID, Operator: "unix:0", Action: action,
		RequestKey: strings.Repeat("b", 64), RecordDigest: hex.EncodeToString(digest[:]),
		ServerDigest: strings.Repeat("c", 64), ObservationDigest: strings.Repeat("d", 64),
	}
	return repo, db, intent
}

func recoveryCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != want {
		t.Fatalf("%s rows: got %d want %d: %v", table, count, want, err)
	}
}

func TestRecoveryDispositionRecordLifecycle(t *testing.T) {
	repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionRecord)
	before, _ := repo.ListPendingDeploymentTransactions(t.Context())
	item, created, err := repo.BeginRecoveryDisposition(t.Context(), intent)
	if err != nil || !created || item.Status != "running" {
		t.Fatal("begin failed", err)
	}
	for _, table := range []string{"recovery_dispositions", "recovery_claims", "recovery_events"} {
		recoveryCount(t, db, table, 1)
	}
	reopened := NewSQLDeploymentTransactionRepository(db)
	if claimed, err := reopened.HasRecoveryClaim(t.Context(), intent.TransactionID); err != nil || !claimed {
		t.Fatal("claim lost across repository restart", err)
	}
	if item, created, err := reopened.BeginRecoveryDisposition(t.Context(), intent); err != nil || created || item.Intent.ID != intent.ID {
		t.Fatal("retry may not authorize a second execution", err)
	}
	for range 2 {
		item, err = reopened.FinishRecoveryDisposition(t.Context(), intent.ID, deployment.RecoveryResultObserved)
		if err != nil || item.Status != "succeeded" || item.ResultCode != deployment.RecoveryResultObserved {
			t.Fatal("completion/retry failed", err)
		}
	}
	recoveryCount(t, db, "recovery_claims", 0)
	recoveryCount(t, db, "recovery_events", 2)
	after, _ := repo.ListPendingDeploymentTransactions(t.Context())
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("recording observation modified original transaction or manufactured evidence")
	}
	if _, created, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil || created {
		t.Fatal("completed request replayed", err)
	}
	if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, deployment.RecoveryResultUncertain); !errors.Is(err, deployment.ErrRecoveryDispositionConflict) {
		t.Fatal("changed terminal outcome accepted", err)
	}
}

func TestRecoveryDispositionRequestBinding(t *testing.T) {
	for _, change := range []string{"transaction", "server", "operator", "action", "record", "configuration", "observation"} {
		t.Run(change, func(t *testing.T) {
			repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionRecord)
			if _, _, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil {
				t.Fatal(err)
			}
			changed := intent
			switch change {
			case "transaction":
				changed.TransactionID = "different"
			case "server":
				changed.ServerID = "different"
			case "operator":
				changed.Operator = "unix:1"
			case "action":
				changed.Action = deployment.RecoveryActionCommit
			case "record":
				changed.RecordDigest = strings.Repeat("e", 64)
			case "configuration":
				changed.ServerDigest = strings.Repeat("e", 64)
			case "observation":
				changed.ObservationDigest = strings.Repeat("e", 64)
			}
			if _, created, err := repo.BeginRecoveryDisposition(t.Context(), changed); !errors.Is(err, deployment.ErrRecoveryDispositionConflict) || created {
				t.Fatal("conflicting idempotency key accepted", err)
			}
			changed = intent
			changed.ID = strings.Repeat("f", 64)
			if item, created, err := repo.BeginRecoveryDisposition(t.Context(), changed); err != nil || created || item.Intent.ID != intent.ID {
				t.Fatal("same request must return original ID", err)
			}
			recoveryCount(t, db, "recovery_dispositions", 1)
		})
	}
}

func TestRecoveryDispositionBeginFailuresAreAtomic(t *testing.T) {
	for _, failure := range []string{"recovery_dispositions", "recovery_claims", "recovery_events", "record_changed", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionRecord)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch failure {
			case "record_changed":
				if _, err := db.ExecContext(t.Context(), `UPDATE deployment_transactions SET message = 'changed'`); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			default:
				if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_begin BEFORE INSERT ON `+failure+` BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, created, err := repo.BeginRecoveryDisposition(ctx, intent); err == nil || created {
				t.Fatal("failed start authorized execution")
			}
			for _, table := range []string{"recovery_dispositions", "recovery_claims", "recovery_events"} {
				recoveryCount(t, db, table, 0)
			}
		})
	}
}

func TestRecoveryDispositionUncertainKeepsClaim(t *testing.T) {
	repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionCommit)
	if _, _, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, deployment.RecoveryResultUncertain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE recovery_claims SET created_at = '2000-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	reopened := NewSQLDeploymentTransactionRepository(db)
	intent.ID, intent.RequestKey = strings.Repeat("e", 64), strings.Repeat("f", 64)
	if _, created, err := reopened.BeginRecoveryDisposition(t.Context(), intent); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) || created {
		t.Fatal("old uncertain claim must not auto-expire", err)
	}
	recoveryCount(t, db, "recovery_dispositions", 1)
	recoveryCount(t, db, "recovery_claims", 1)
	recoveryCount(t, db, "recovery_events", 2)
}

func TestRecoveryDispositionConcurrentRequests(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(fmt.Sprint(sameKey), func(t *testing.T) {
			repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionRecord)
			db.SetMaxOpenConns(1)
			var started atomic.Int32
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					candidate := intent
					if !sameKey {
						candidate.ID, candidate.RequestKey = fmt.Sprintf("%064x", i+1), fmt.Sprintf("%064x", i+10)
					}
					_, created, err := repo.BeginRecoveryDisposition(t.Context(), candidate)
					if created {
						started.Add(1)
					}
					if sameKey && err != nil {
						t.Errorf("same request failed: %v", err)
					}
					if !sameKey && err != nil && !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
						t.Errorf("unexpected competing request error: %v", err)
					}
				}()
			}
			wg.Wait()
			if started.Load() != 1 {
				t.Fatal("multiple remote executions authorized")
			}
			for _, table := range []string{"recovery_dispositions", "recovery_claims", "recovery_events"} {
				recoveryCount(t, db, table, 1)
			}
		})
	}
}

func TestRecoveryDispositionClaimSurvivesDatabaseReopen(t *testing.T) {
	repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionCommit)
	if _, _, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var name, path string
	if err := db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	repo = NewSQLDeploymentTransactionRepository(reopened)
	if claimed, err := repo.HasRecoveryClaim(t.Context(), intent.TransactionID); err != nil || !claimed {
		t.Fatal("incomplete operation lost its persistent claim", err)
	}
	if existing, created, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil || created || existing.Status != "running" {
		t.Fatal("reopened request must not authorize replay", err)
	}
	intent.ID, intent.RequestKey = strings.Repeat("e", 64), strings.Repeat("f", 64)
	if _, _, err := repo.BeginRecoveryDisposition(t.Context(), intent); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
		t.Fatal("restart released unfinished claim", err)
	}
}
