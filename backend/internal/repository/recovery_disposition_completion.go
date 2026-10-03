package repository

import (
	"context"
	"database/sql"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// FinishRecoveryDisposition 仅由完成现场核验的内部执行器调用，不接受公网客户端结果。
// 不确定结果保留占用；成功结果、必要的原事务终态与结束审计在同一事务内提交。
func (r *SQLDeploymentTransactionRepository) FinishRecoveryDisposition(ctx context.Context, id, resultCode string) (deployment.RecoveryDisposition, error) {
	if r == nil || r.db == nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	defer tx.Rollback()
	query := `SELECT ` + recoveryDispositionColumns + ` FROM recovery_dispositions WHERE id = ?`
	if r.driver != "sqlite" {
		query += " FOR UPDATE"
	}
	item, err := scanRecoveryDisposition(tx.QueryRowContext(ctx, r.query(query), id))
	if err != nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	if !item.Intent.AllowsResult(resultCode) {
		return deployment.RecoveryDisposition{}, deployment.ErrRecoveryDispositionInvalid
	}
	if item.Status != "running" {
		if item.ResultCode == resultCode {
			return item, nil
		}
		return deployment.RecoveryDisposition{}, deployment.ErrRecoveryDispositionConflict
	}
	var claim, start string
	if err := tx.QueryRowContext(ctx, r.query(`SELECT disposition_id FROM recovery_claims WHERE transaction_id = ?`), item.Intent.TransactionID).Scan(&claim); err != nil || claim != id {
		return deployment.RecoveryDisposition{}, deployment.ErrRecoveryDispositionConflict
	}
	if err := tx.QueryRowContext(ctx, r.query(`SELECT event_type FROM recovery_events WHERE disposition_id = ? AND event_type = 'started'`), id).Scan(&start); err != nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	if item.Intent.SourceDispositionID != "" {
		if err := r.validateReconciliationSource(ctx, tx, item.Intent); err != nil {
			return deployment.RecoveryDisposition{}, err
		}
	}
	now := time.Now().UTC()
	if err := r.finishRecoveryOriginal(ctx, tx, item.Intent, resultCode, now); err != nil {
		return deployment.RecoveryDisposition{}, err
	}
	item.Status = "succeeded"
	if resultCode == deployment.RecoveryResultRejected || resultCode == deployment.RecoveryResultUncertain {
		item.Status = resultCode
	}
	result, err := tx.ExecContext(ctx, r.query(`UPDATE recovery_dispositions SET status = ?, result_code = ?, updated_at = ? WHERE id = ? AND status = 'running'`), item.Status, resultCode, now, id)
	if err != nil || !oneRecoveryRow(result) {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	if _, err := tx.ExecContext(ctx, r.query(`INSERT INTO recovery_events (disposition_id, event_type, result_code, created_at) VALUES (?, 'finished', ?, ?)`), id, resultCode, now); err != nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	if item.Intent.SourceDispositionID != "" && (resultCode == deployment.RecoveryResultRejected || resultCode == deployment.RecoveryResultUncertain) {
		// 只读核查未通过时归还原占用，保留两次审计；绝不解锁原事务。
		result, err := tx.ExecContext(ctx, r.query(`UPDATE recovery_claims SET disposition_id = ?, created_at = ? WHERE transaction_id = ? AND disposition_id = ?`), item.Intent.SourceDispositionID, now, item.Intent.TransactionID, id)
		if err != nil || !oneRecoveryRow(result) {
			return deployment.RecoveryDisposition{}, dispositionError(ctx)
		}
	} else if resultCode != deployment.RecoveryResultUncertain {
		result, err := tx.ExecContext(ctx, r.query(`DELETE FROM recovery_claims WHERE transaction_id = ? AND disposition_id = ?`), item.Intent.TransactionID, id)
		if err != nil || !oneRecoveryRow(result) {
			return deployment.RecoveryDisposition{}, dispositionError(ctx)
		}
	}
	if err := tx.Commit(); err != nil {
		return deployment.RecoveryDisposition{}, dispositionError(ctx)
	}
	item.ResultCode, item.UpdatedAt = resultCode, now
	return item, nil
}

func oneRecoveryRow(result sql.Result) bool {
	if result == nil {
		return false
	}
	count, err := result.RowsAffected()
	return err == nil && count == 1
}

func (r *SQLDeploymentTransactionRepository) finishRecoveryOriginal(ctx context.Context, tx *sql.Tx, intent deployment.RecoveryDispositionIntent, resultCode string, now time.Time) error {
	if resultCode != deployment.RecoveryResultCommitted && resultCode != deployment.RecoveryResultRolledBack {
		// 只记录核查、核验拒绝与不确定结果均不改变原事务；不能以已阅读代替恢复成功。
		return nil
	}
	record, err := r.lockRecoveryTransaction(ctx, tx, intent.TransactionID)
	if err != nil {
		return dispositionError(ctx)
	}
	phase, status, message := deployment.TransactionPhaseCommitVerified, deployment.TransactionStatusCommitted, "人工处置已完成提交核验"
	if resultCode == deployment.RecoveryResultRolledBack {
		phase, status, message = deployment.TransactionPhaseRollbackVerified, deployment.TransactionStatusRolledBack, "人工处置已完成回滚核验"
	}
	if record.ServerID != intent.ServerID || record.Evidence == nil || record.Phase != phase ||
		(record.Status != deployment.TransactionStatusPending && record.Status != deployment.TransactionStatusRecoveryRequired) {
		return deployment.ErrRecoveryDispositionConflict
	}
	if intent.SourceDispositionID != "" && !intent.MatchesRecord(record) {
		return deployment.ErrRecoveryReviewChanged
	}
	result, err := tx.ExecContext(ctx, r.query(`UPDATE deployment_transactions SET status = ?, phase = ?, message = ?, updated_at = ?
		WHERE id = ? AND phase = ? AND status IN ('pending', 'recovery_required')`), status, status, message, now, intent.TransactionID, phase)
	if err != nil || !oneRecoveryRow(result) {
		return dispositionError(ctx)
	}
	return nil
}
