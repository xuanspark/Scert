package repository

import (
	"errors"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestRecoveryDispositionVerifiedCompletionAtomicity(t *testing.T) {
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		for _, failure := range []string{"none", "original", "result", "audit", "release"} {
			t.Run(string(action)+"/"+failure, func(t *testing.T) {
				repo, db, intent := recoveryDispositionFixture(t, action)
				if _, _, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil {
					t.Fatal(err)
				}
				phase, resultCode := deployment.TransactionPhaseCommitVerified, deployment.RecoveryResultCommitted
				if action == deployment.RecoveryActionRollback {
					phase, resultCode = deployment.TransactionPhaseRollbackVerified, deployment.RecoveryResultRolledBack
				}
				if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, resultCode); !errors.Is(err, deployment.ErrRecoveryDispositionConflict) {
					t.Fatal("unverified original finalized", err)
				}
				if _, err := db.ExecContext(t.Context(), `UPDATE deployment_transactions SET phase = ?`, phase); err != nil {
					t.Fatal(err)
				}
				triggers := map[string]string{
					"original": "BEFORE UPDATE ON deployment_transactions",
					"result":   "BEFORE UPDATE ON recovery_dispositions",
					"audit":    "BEFORE INSERT ON recovery_events",
					"release":  "BEFORE DELETE ON recovery_claims",
				}
				if trigger, ok := triggers[failure]; ok {
					if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_finish `+trigger+` BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
						t.Fatal(err)
					}
				}
				item, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, resultCode)
				var status, storedPhase string
				if readErr := db.QueryRowContext(t.Context(), `SELECT status, phase FROM deployment_transactions WHERE id = ?`, intent.TransactionID).Scan(&status, &storedPhase); readErr != nil {
					t.Fatal(readErr)
				}
				if failure == "none" {
					if err != nil || item.Status != "succeeded" || status != resultCode || storedPhase != resultCode {
						t.Fatal("atomic completion failed", err)
					}
					recoveryCount(t, db, "recovery_claims", 0)
					recoveryCount(t, db, "recovery_events", 2)
					if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, resultCode); err != nil {
						t.Fatal(err)
					}
				} else {
					if err == nil || status != deployment.TransactionStatusRecoveryRequired || storedPhase != phase {
						t.Fatal("failed completion changed original")
					}
					stored, err := repo.GetRecoveryDisposition(t.Context(), intent.ID)
					if err != nil || stored.Status != "running" || stored.ResultCode != "" {
						t.Fatal("partial disposition outcome saved", err)
					}
					recoveryCount(t, db, "recovery_claims", 1)
					recoveryCount(t, db, "recovery_events", 1)
				}
			})
		}
	}
}

func TestRecoveryDispositionRejectsCrossActionCompletion(t *testing.T) {
	repo, db, intent := recoveryDispositionFixture(t, deployment.RecoveryActionRecord)
	if _, _, err := repo.BeginRecoveryDisposition(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{deployment.RecoveryResultCommitted, deployment.RecoveryResultRolledBack, "unknown", ""} {
		if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, result); !errors.Is(err, deployment.ErrRecoveryDispositionInvalid) {
			t.Fatal("invalid result accepted", err)
		}
	}
	if _, err := repo.FinishRecoveryDisposition(t.Context(), intent.ID, deployment.RecoveryResultRejected); err != nil {
		t.Fatal(err)
	}
	recoveryCount(t, db, "recovery_claims", 0)
	pending, err := repo.ListPendingDeploymentTransactions(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Evidence != nil {
		t.Fatal("rejected review changed original", err)
	}
}
