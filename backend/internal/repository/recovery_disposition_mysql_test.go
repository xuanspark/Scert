package repository

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestRecoveryDispositionMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionRecord, deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		for _, auditFailure := range []bool{false, true} {
			name := string(action)
			if auditFailure {
				name += "/audit_failure"
			}
			t.Run(name, func(t *testing.T) {
				cfg := server.Database(t)
				db, err := database.Open(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
					t.Fatal(err)
				}
				repo, raw, intent := seedRecoveryDisposition(t, NewSQLDeploymentTransactionRepository(db.DB, cfg.Driver), db.DB, action)
				if _, created, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil || !created {
					t.Fatal("start failed", err)
				}
				if _, created, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil || created {
					t.Fatal("retry authorized replay", err)
				}
				changed := intent
				changed.Operator = "unix:1"
				if _, _, err := repo.BeginRecoveryDisposition(t.Context(), changed); !errors.Is(err, deployment.ErrRecoveryDispositionConflict) {
					t.Fatal("conflicting replay accepted", err)
				}
				changed = intent
				changed.ID, changed.RequestKey = strings.Repeat("e", 64), strings.Repeat("f", 64)
				if _, _, err := repo.BeginRecoveryDisposition(t.Context(), changed); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
					t.Fatal("duplicate claim accepted", err)
				}
				result, phase := deployment.RecoveryResultObserved, deployment.TransactionPhaseInstalled
				if action == deployment.RecoveryActionCommit {
					result, phase = deployment.RecoveryResultCommitted, deployment.TransactionPhaseCommitVerified
				}
				if action == deployment.RecoveryActionRollback {
					result, phase = deployment.RecoveryResultRolledBack, deployment.TransactionPhaseRollbackVerified
				}
				if _, err := raw.ExecContext(t.Context(), `UPDATE deployment_transactions SET phase = ? WHERE id = ?`, phase, intent.TransactionID); err != nil {
					t.Fatal(err)
				}
				if auditFailure {
					parsed, err := mysql.ParseDSN(cfg.DSN)
					if err != nil || !strings.HasPrefix(parsed.DBName, "scert_test_") {
						t.Fatal("invalid isolated database configuration")
					}
					schema := "`" + strings.ReplaceAll(parsed.DBName, "`", "``") + "`"
					if _, err := server.Admin().ExecContext(t.Context(), `CREATE TRIGGER `+schema+`.reject_finish BEFORE INSERT ON `+schema+`.recovery_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected'`); err != nil {
						t.Fatal(err)
					}
				}
				item, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, result)
				var status, storedPhase string
				if readErr := raw.QueryRowContext(t.Context(), `SELECT status, phase FROM deployment_transactions WHERE id = ?`, intent.TransactionID).Scan(&status, &storedPhase); readErr != nil {
					t.Fatal(readErr)
				}
				if auditFailure {
					if err == nil || status != deployment.TransactionStatusRecoveryRequired || storedPhase != phase {
						t.Fatal("audit failure committed partial outcome")
					}
					stored, err := repo.GetRecoveryDisposition(t.Context(), intent.ID)
					if err != nil || stored.Status != "running" {
						t.Fatal("failed completion changed disposition", err)
					}
					recoveryCount(t, raw, "recovery_events", 1)
					recoveryCount(t, raw, "recovery_claims", 1)
				} else {
					if err != nil || item.Status != "succeeded" {
						t.Fatal("completion failed", err)
					}
					if action != deployment.RecoveryActionRecord && (status != result || storedPhase != result) {
						t.Fatal("original terminal state missing")
					}
					if action == deployment.RecoveryActionRecord && (status != deployment.TransactionStatusRecoveryRequired || storedPhase != phase) {
						t.Fatal("observation changed original")
					}
					if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, result); err != nil {
						t.Fatal("completion retry failed", err)
					}
					recoveryCount(t, raw, "recovery_events", 2)
					recoveryCount(t, raw, "recovery_claims", 0)
				}
			})
		}
	}
}
