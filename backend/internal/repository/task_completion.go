package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// TaskCompletion 同时关闭任务与对应尝试，避免只保存其中一方的终态。
type TaskCompletion struct {
	TaskID         string
	AttemptID      string
	Status         string
	Stage          string
	Message        string
	StartedAt      *time.Time
	FinishedAt     time.Time
	Report         *deployment.Report
	Validation     *certificate.ValidationResult
	AuditAction    string
	Retry          bool
	WithoutAttempt bool
}

func (r *SQLTaskRepository) FinishTaskAttempt(ctx context.Context, completion TaskCompletion) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	completion.TaskID = strings.TrimSpace(completion.TaskID)
	completion.AttemptID = strings.TrimSpace(completion.AttemptID)
	completion.Stage = strings.TrimSpace(completion.Stage)
	if completion.TaskID == "" || (completion.AttemptID == "" && !completion.WithoutAttempt) || completion.Stage == "" || completion.FinishedAt.IsZero() {
		return errors.New("任务终态记录不完整")
	}
	switch completion.Status {
	case "success", "failed", "cancelled", "partial_success":
	default:
		return errors.New("任务终态无效")
	}
	if completion.WithoutAttempt && (completion.AttemptID != "" || completion.Report != nil || completion.Validation != nil || completion.Retry || (completion.Status != "failed" && completion.Status != "cancelled")) {
		return errors.New("无活动尝试的收尾参数无效")
	}
	if completion.Retry && (completion.Status != "failed" || completion.AuditAction != "") {
		return errors.New("重试只能保存失败尝试且不能发布完成审计")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始任务收尾事务失败：%w", err)
	}
	defer tx.Rollback()
	taskStatus, taskStage := completion.Status, completion.Stage
	var taskFinished any = completion.FinishedAt.UTC()
	activeStates := "('queued', 'running', 'retry_wait', 'cancelling')"
	if completion.Retry {
		taskStatus, taskStage, taskFinished = "retry_wait", "retry_wait", nil
		// 已受理的取消不能被重试重新激活。
		activeStates = "('running')"
	}
	// 与取消、重试保持任务在前、尝试在后的加锁顺序。旧尝试或跨任务尝试必须整体回滚。
	result, err := tx.ExecContext(ctx, r.query(`UPDATE tasks
		SET status = ?, stage = ?, message = ?, started_at = COALESCE(?, started_at), finished_at = ?, updated_at = ?
		WHERE id = ? AND status IN `+activeStates),
		taskStatus, taskStage, completion.Message, nullableTime(completion.StartedAt),
		taskFinished, completion.FinishedAt.UTC(), completion.TaskID)
	if err != nil {
		return fmt.Errorf("保存任务终态失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("读取任务收尾结果失败：%w", err)
	} else if count != 1 {
		return sql.ErrNoRows
	}
	if err := r.finishCompletionAttempt(ctx, tx, completion); err != nil {
		return err
	}
	if completion.Report != nil {
		if err := r.recordCompletionReport(ctx, tx, completion); err != nil {
			return err
		}
	}
	if completion.Validation != nil {
		if err := r.recordCompletionValidation(ctx, tx, completion); err != nil {
			return err
		}
	}
	if completion.AuditAction != "" {
		if err := r.recordCompletionAudit(ctx, tx, completion); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务收尾事务失败：%w", err)
	}
	return nil
}

func (r *SQLTaskRepository) finishCompletionAttempt(ctx context.Context, tx *sql.Tx, completion TaskCompletion) error {
	if completion.WithoutAttempt {
		var active int
		if err := tx.QueryRowContext(ctx, r.query(`SELECT COUNT(*) FROM task_attempts
			WHERE task_id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')`), completion.TaskID).Scan(&active); err != nil {
			return fmt.Errorf("检查活动尝试失败：%w", err)
		}
		if active != 0 {
			return errors.New("任务仍有活动尝试，不能单独收尾")
		}
		return nil
	}
	result, err := tx.ExecContext(ctx, r.query(`UPDATE task_attempts
		SET status = ?, stage = ?, message = ?, finished_at = ?
		WHERE id = ? AND task_id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')`),
		completion.Status, completion.Stage, completion.Message, completion.FinishedAt.UTC(), completion.AttemptID, completion.TaskID)
	if err != nil {
		return fmt.Errorf("保存任务尝试终态失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("读取任务尝试收尾结果失败：%w", err)
	} else if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
