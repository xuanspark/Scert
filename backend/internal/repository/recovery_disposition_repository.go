package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

var _ deployment.RecoveryDispositionJournal = (*SQLDeploymentTransactionRepository)(nil)

const recoveryDispositionColumns = `id, transaction_id, server_id, operator_id, action, request_key,
	record_digest, server_digest, observation_digest, status, result_code, created_at, updated_at, source_disposition_id`

func scanRecoveryDisposition(row deploymentTransactionScanner) (deployment.RecoveryDisposition, error) {
	var item deployment.RecoveryDisposition
	i := &item.Intent
	err := row.Scan(&i.ID, &i.TransactionID, &i.ServerID, &i.Operator, &i.Action, &i.RequestKey,
		&i.RecordDigest, &i.ServerDigest, &i.ObservationDigest, &item.Status, &item.ResultCode, &item.CreatedAt, &item.UpdatedAt, &i.SourceDispositionID)
	return item, err
}

func dispositionError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return deployment.ErrRecoveryDispositionUnavailable
}

func (r *SQLDeploymentTransactionRepository) GetRecoveryDisposition(ctx context.Context, id string) (deployment.RecoveryDisposition, error) {
	if r == nil || r.db == nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	item, err := scanRecoveryDisposition(r.db.QueryRowContext(ctx, r.query(`SELECT `+recoveryDispositionColumns+` FROM recovery_dispositions WHERE id = ?`), id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		err = dispositionError(ctx)
	}
	return item, err
}

func (r *SQLDeploymentTransactionRepository) HasRecoveryClaim(ctx context.Context, transactionID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, dispositionError(ctx)
	}
	var id string
	err := r.db.QueryRowContext(ctx, r.query(`SELECT disposition_id FROM recovery_claims WHERE transaction_id = ?`), transactionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, dispositionError(ctx)
	}
	return true, nil
}

func (r *SQLDeploymentTransactionRepository) BeginRecoveryDisposition(ctx context.Context, intent deployment.RecoveryDispositionIntent) (deployment.RecoveryDisposition, bool, error) {
	if err := intent.Validate(); err != nil {
		return deployment.RecoveryDisposition{}, false, err
	}
	if r == nil || r.db == nil {
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	// 重试先读取既有结果；唯一约束负责并发首次请求，不能靠进程内缓存判定幂等。
	if existing, err := r.findRecoveryRequest(ctx, intent.RequestKey); err == nil {
		return recoveryRetry(existing, intent)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	item := deployment.RecoveryDisposition{Intent: intent, Status: "running", CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, r.query(`INSERT INTO recovery_dispositions (`+recoveryDispositionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		intent.ID, intent.TransactionID, intent.ServerID, intent.Operator, intent.Action, intent.RequestKey,
		intent.RecordDigest, intent.ServerDigest, intent.ObservationDigest, item.Status, "", now, now, intent.SourceDispositionID)
	if err != nil {
		_ = tx.Rollback()
		if existing, readErr := r.findRecoveryRequest(ctx, intent.RequestKey); readErr == nil {
			return recoveryRetry(existing, intent)
		}
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	if err := r.claimRecoveryDisposition(ctx, tx, intent, now); err != nil {
		_ = tx.Rollback()
		if claimed, readErr := r.HasRecoveryClaim(ctx, intent.TransactionID); readErr == nil && claimed {
			return deployment.RecoveryDisposition{}, false, deployment.ErrRecoveryDispositionBusy
		}
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	record, err := r.lockRecoveryTransaction(ctx, tx, intent.TransactionID)
	if err != nil {
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	if !intent.MatchesRecord(record) {
		return deployment.RecoveryDisposition{}, false, deployment.ErrRecoveryReviewChanged
	}
	if _, err := tx.ExecContext(ctx, r.query(`INSERT INTO recovery_events (disposition_id, event_type, result_code, created_at) VALUES (?, 'started', '', ?)`), intent.ID, now); err != nil {
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	if err := tx.Commit(); err != nil {
		// 提交结果不确定时不得授权动作；下次凭同一请求键读取持久记录。
		return deployment.RecoveryDisposition{}, false, dispositionError(ctx)
	}
	return item, true, nil
}

func (r *SQLDeploymentTransactionRepository) claimRecoveryDisposition(ctx context.Context, tx *sql.Tx, intent deployment.RecoveryDispositionIntent, now time.Time) error {
	if intent.SourceDispositionID == "" {
		_, err := tx.ExecContext(ctx, r.query(`INSERT INTO recovery_claims (transaction_id, disposition_id, created_at) VALUES (?, ?, ?)`), intent.TransactionID, intent.ID, now)
		return err
	}
	if err := r.validateReconciliationSource(ctx, tx, intent); err != nil {
		return err
	}
	// 只从指定的已结束处置接管占用；运行中、其他处置或丢失的占用均不接管。
	result, err := tx.ExecContext(ctx, r.query(`UPDATE recovery_claims SET disposition_id = ?, created_at = ? WHERE transaction_id = ? AND disposition_id = ?`), intent.ID, now, intent.TransactionID, intent.SourceDispositionID)
	if err != nil || !oneRecoveryRow(result) {
		return deployment.ErrRecoveryDispositionBusy
	}
	return nil
}

func (r *SQLDeploymentTransactionRepository) validateReconciliationSource(ctx context.Context, tx *sql.Tx, intent deployment.RecoveryDispositionIntent) error {
	query := `SELECT ` + recoveryDispositionColumns + ` FROM recovery_dispositions WHERE id = ?`
	if r.driver != "sqlite" {
		query += " FOR UPDATE"
	}
	source, err := scanRecoveryDisposition(tx.QueryRowContext(ctx, r.query(query), intent.SourceDispositionID))
	if err != nil || !intent.Reconciles(source) {
		return deployment.ErrRecoveryDispositionConflict
	}
	var count int
	if err := tx.QueryRowContext(ctx, r.query(`SELECT COUNT(*) FROM recovery_events WHERE disposition_id = ? AND
		((event_type = 'started' AND result_code = '') OR (event_type = 'finished' AND result_code = 'uncertain'))`), intent.SourceDispositionID).Scan(&count); err != nil || count != 2 {
		return deployment.ErrRecoveryDispositionConflict
	}
	return nil
}

func recoveryRetry(existing deployment.RecoveryDisposition, intent deployment.RecoveryDispositionIntent) (deployment.RecoveryDisposition, bool, error) {
	if !existing.Intent.SameRequest(intent) {
		return deployment.RecoveryDisposition{}, false, deployment.ErrRecoveryDispositionConflict
	}
	return existing, false, nil
}

func (r *SQLDeploymentTransactionRepository) findRecoveryRequest(ctx context.Context, key string) (deployment.RecoveryDisposition, error) {
	return scanRecoveryDisposition(r.db.QueryRowContext(ctx, r.query(`SELECT `+recoveryDispositionColumns+` FROM recovery_dispositions WHERE request_key = ?`), key))
}

func (r *SQLDeploymentTransactionRepository) lockRecoveryTransaction(ctx context.Context, tx *sql.Tx, id string) (deployment.TransactionRecord, error) {
	query := `SELECT id, task_id, attempt_id, attempt_number, result_index,
		server_id, server_name, certificate_path, certificate_chain_path, private_key_path,
		temporary_paths_json, installed_paths_json, backups_json, lock_paths_json,
		reload_attempted, phase, status, message, updated_at, recovery_evidence_json
		FROM deployment_transactions WHERE id = ?`
	if r.driver != "sqlite" {
		query += " FOR UPDATE"
	}
	return scanDeploymentTransaction(tx.QueryRowContext(ctx, r.query(query), id))
}
