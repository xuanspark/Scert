package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// ProjectTaskReader 只返回当前账户有权查看的任务和任务尝试。
type ProjectTaskReader interface {
	ListVisibleTasks(context.Context, string, string, int) ([]Task, error)
	GetVisibleTask(context.Context, string, string) (Task, error)
	ListVisibleTaskAttempts(context.Context, string, string) ([]TaskAttempt, error)
	ListVisibleTaskDeploymentResults(context.Context, string, string) ([]TaskDeploymentResult, error)
	CancelVisibleTask(context.Context, string, string, string, *time.Time) error
	RetryVisibleTask(context.Context, string, string, *time.Time) (Task, TaskAttempt, error)
}

var ErrTaskNotRetryable = errors.New("任务当前不可重试")

var _ ProjectTaskReader = (*SQLTaskRepository)(nil)

func taskVisibilityPredicate(alias string, permission domain.ProjectPermission) (string, []any) {
	roles := []domain.ProjectRole{domain.ProjectRoleAdmin}
	if permission == domain.PermissionTaskRead {
		roles = []domain.ProjectRole{
			domain.ProjectRoleAdmin,
			domain.ProjectRoleAuditViewer,
			domain.ProjectRoleReadOnly,
		}
	}
	if permission == domain.PermissionTaskRetry {
		roles = []domain.ProjectRole{domain.ProjectRoleAdmin}
	}
	placeholders := make([]string, 0, len(roles))
	args := []any{""}
	for _, role := range roles {
		placeholders = append(placeholders, "?")
		args = append(args, string(role))
	}
	return `EXISTS (
		SELECT 1 FROM users task_actor
		WHERE task_actor.id = ? AND task_actor.status = 'active'
		AND (task_actor.role IN ('super_admin', 'admin') OR (
			task_actor.role = 'user' AND ` + alias + `.scope_type = 'project'
			AND EXISTS (SELECT 1 FROM project_members task_member
				WHERE task_member.project_id = ` + alias + `.project_id
				AND task_member.user_id = task_actor.id
				AND task_member.role IN (` + strings.Join(placeholders, ", ") + `))))
	)`, args
}

func (r *SQLTaskRepository) ListVisibleTasks(ctx context.Context, actorID, domainID string, limit int) ([]Task, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	if strings.TrimSpace(actorID) == "" {
		return nil, ErrProjectAccessDenied
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	predicate, args := taskVisibilityPredicate("tasks", domain.PermissionTaskRead)
	args[0] = strings.TrimSpace(actorID)
	query := taskSelect + ` WHERE ` + predicate
	if domainID = strings.TrimSpace(domainID); domainID != "" {
		query += " AND tasks.domain_id = ?"
		args = append(args, domainID)
	}
	query += fmt.Sprintf(" ORDER BY tasks.created_at DESC, tasks.id DESC LIMIT %d", limit)
	rows, err := r.db.QueryContext(ctx, r.query(query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询可见任务失败：%w", err)
	}
	defer rows.Close()
	items := make([]Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取可见任务失败：%w", err)
	}
	return items, nil
}

func (r *SQLTaskRepository) GetVisibleTask(ctx context.Context, actorID, taskID string) (Task, error) {
	if r == nil || r.db == nil {
		return Task{}, errors.New("数据库连接不能为空")
	}
	predicate, args := taskVisibilityPredicate("tasks", domain.PermissionTaskRead)
	args[0] = strings.TrimSpace(actorID)
	args = append(args, strings.TrimSpace(taskID))
	return scanTask(r.db.QueryRowContext(ctx, r.query(taskSelect+` WHERE `+predicate+` AND tasks.id = ?`), args...))
}

func (r *SQLTaskRepository) ListVisibleTaskAttempts(ctx context.Context, actorID, taskID string) ([]TaskAttempt, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	predicate, args := taskVisibilityPredicate("tasks", domain.PermissionTaskRead)
	args[0] = strings.TrimSpace(actorID)
	args = append(args, strings.TrimSpace(taskID))
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT attempts.id, attempts.task_id, attempts.attempt_number,
		attempts.stage, attempts.status, attempts.message, attempts.started_at, attempts.finished_at,
		attempts.created_at, COALESCE(tasks.scope_type, 'unknown'), COALESCE(tasks.project_id, '')
		FROM task_attempts attempts JOIN tasks ON tasks.id = attempts.task_id
		WHERE `+predicate+` AND attempts.task_id = ? ORDER BY attempts.attempt_number`), args...)
	if err != nil {
		return nil, fmt.Errorf("查询可见任务尝试失败：%w", err)
	}
	defer rows.Close()
	items := make([]TaskAttempt, 0)
	for rows.Next() {
		item, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取可见任务尝试失败：%w", err)
	}
	return items, nil
}

func (r *SQLTaskRepository) ListVisibleTaskDeploymentResults(ctx context.Context, actorID, taskID string) ([]TaskDeploymentResult, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	predicate, args := taskVisibilityPredicate("tasks", domain.PermissionTaskRead)
	args[0] = strings.TrimSpace(actorID)
	args = append(args, strings.TrimSpace(taskID))
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT results.task_id, results.attempt_id, results.attempt_number,
		results.result_index, results.server_id, results.server_name, results.status, results.message,
		results.backup_id, results.created_at
		FROM task_deployment_results results JOIN tasks ON tasks.id = results.task_id
		WHERE `+predicate+` AND results.task_id = ? ORDER BY results.attempt_number, results.result_index`), args...)
	if err != nil {
		return nil, fmt.Errorf("查询可见部署结果失败：%w", err)
	}
	defer rows.Close()
	items := make([]TaskDeploymentResult, 0)
	for rows.Next() {
		var item TaskDeploymentResult
		if err := rows.Scan(&item.TaskID, &item.AttemptID, &item.AttemptNumber, &item.ResultIndex,
			&item.ServerID, &item.Server, &item.Status, &item.Message, &item.BackupID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取可见部署结果失败：%w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取可见部署结果列表失败：%w", err)
	}
	return items, nil
}

func (r *SQLTaskRepository) CancelVisibleTask(ctx context.Context, actorID, taskID, message string, finishedAt *time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始取消任务事务失败：%w", err)
	}
	rollback := func() { _ = tx.Rollback() }
	predicate, predicateArgs := taskVisibilityPredicate("tasks", domain.PermissionTaskCancel)
	predicateArgs[0] = strings.TrimSpace(actorID)
	args := []any{message, nullableTime(finishedAt), time.Now().UTC(), strings.TrimSpace(taskID)}
	args = append(args, predicateArgs...)
	result, err := tx.ExecContext(ctx, r.query(`UPDATE tasks SET status = 'cancelled', message = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling') AND `+predicate), args...)
	if err != nil {
		rollback()
		return fmt.Errorf("取消任务失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		rollback()
		return fmt.Errorf("读取任务取消结果失败：%w", err)
	}
	if count == 0 {
		item, getErr := scanTask(tx.QueryRowContext(ctx, r.query(taskSelect+` WHERE id = ?`), strings.TrimSpace(taskID)))
		if errors.Is(getErr, sql.ErrNoRows) {
			rollback()
			return sql.ErrNoRows
		}
		if getErr != nil {
			rollback()
			return getErr
		}
		if item.Status == "cancelled" {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("提交重复取消任务事务失败：%w", err)
			}
			return nil
		}
		rollback()
		return ErrProjectAccessDenied
	}
	if _, err := tx.ExecContext(ctx, r.query(`UPDATE task_attempts SET status = 'cancelled', message = ?, finished_at = ?
		WHERE task_id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')`), message, nullableTime(finishedAt), strings.TrimSpace(taskID)); err != nil {
		rollback()
		return fmt.Errorf("取消任务尝试失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交取消任务事务失败：%w", err)
	}
	return nil
}

func (r *SQLTaskRepository) RetryVisibleTask(ctx context.Context, actorID, taskID string, startedAt *time.Time) (Task, TaskAttempt, error) {
	if r == nil || r.db == nil {
		return Task{}, TaskAttempt{}, errors.New("数据库连接不能为空")
	}
	actorID = strings.TrimSpace(actorID)
	taskID = strings.TrimSpace(taskID)
	if actorID == "" || taskID == "" {
		return Task{}, TaskAttempt{}, ErrProjectAccessDenied
	}
	if startedAt == nil {
		now := time.Now().UTC()
		startedAt = &now
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, TaskAttempt{}, fmt.Errorf("开始重试任务事务失败：%w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	predicate, predicateArgs := taskVisibilityPredicate("tasks", domain.PermissionTaskRetry)
	predicateArgs[0] = actorID
	updateArgs := []any{
		actorID, nullableTime(startedAt), time.Now().UTC(), taskID,
	}
	updateArgs = append(updateArgs, predicateArgs...)
	result, err := tx.ExecContext(ctx, r.query(`UPDATE tasks SET operator_id = ?, status = 'running',
		stage = CASE task_type WHEN 'certificate_deploy' THEN 'certificate_deploy' ELSE 'certificate_issue' END,
		message = CASE task_type WHEN 'certificate_deploy' THEN '正在重试证书部署' ELSE '正在重试证书申请' END,
		started_at = ?, finished_at = NULL, updated_at = ?
		WHERE id = ? AND task_type IN ('certificate_issue', 'certificate_deploy')
		AND (status IN ('failed', 'cancelled') OR (task_type = 'certificate_deploy' AND status = 'partial_success'))
		AND `+predicate), updateArgs...)
	if err != nil {
		rollback()
		return Task{}, TaskAttempt{}, fmt.Errorf("更新重试任务状态失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		rollback()
		return Task{}, TaskAttempt{}, fmt.Errorf("读取重试任务更新结果失败：%w", err)
	}
	if count != 1 {
		item, getErr := scanTask(tx.QueryRowContext(ctx, r.query(taskSelect+` WHERE id = ?`), taskID))
		rollback()
		if errors.Is(getErr, sql.ErrNoRows) {
			return Task{}, TaskAttempt{}, sql.ErrNoRows
		}
		if getErr != nil {
			return Task{}, TaskAttempt{}, getErr
		}
		if !retryableTaskStatus(item) {
			return Task{}, TaskAttempt{}, ErrTaskNotRetryable
		}
		return Task{}, TaskAttempt{}, ErrProjectAccessDenied
	}

	task, err := scanTask(tx.QueryRowContext(ctx, r.query(taskSelect+` WHERE id = ?`), taskID))
	if err != nil {
		rollback()
		return Task{}, TaskAttempt{}, err
	}
	stage := "certificate_issue"
	message := "开始重试证书申请"
	if task.TaskType == "certificate_deploy" {
		stage = "certificate_deploy"
		message = "开始重试证书部署"
	}

	var attemptNumber int
	if err := tx.QueryRowContext(ctx, r.query(`SELECT COALESCE(MAX(attempt_number), 0) + 1 FROM task_attempts WHERE task_id = ?`), taskID).Scan(&attemptNumber); err != nil {
		rollback()
		return Task{}, TaskAttempt{}, fmt.Errorf("读取任务重试次数失败：%w", err)
	}
	attempt := TaskAttempt{
		HistoryScope:  task.HistoryScope,
		ID:            domain.NewID(),
		TaskID:        taskID,
		AttemptNumber: attemptNumber,
		Stage:         stage,
		Status:        "running",
		Message:       message,
		StartedAt:     startedAt,
		CreatedAt:     startedAt.UTC(),
	}
	insertResult, err := tx.ExecContext(ctx, r.query(`INSERT INTO task_attempts
		(id, task_id, attempt_number, stage, status, message, started_at, finished_at, created_at)
		SELECT ?, id, ?, ?, ?, ?, ?, NULL, ? FROM tasks WHERE id = ?`),
		attempt.ID, attempt.AttemptNumber, attempt.Stage, attempt.Status, attempt.Message,
		nullableTime(attempt.StartedAt), attempt.CreatedAt, taskID)
	if err != nil {
		rollback()
		return Task{}, TaskAttempt{}, fmt.Errorf("创建重试任务尝试失败：%w", err)
	}
	if count, err := insertResult.RowsAffected(); err != nil {
		rollback()
		return Task{}, TaskAttempt{}, fmt.Errorf("读取重试任务尝试创建结果失败：%w", err)
	} else if count != 1 {
		rollback()
		return Task{}, TaskAttempt{}, ErrTaskNotRetryable
	}

	if err := tx.Commit(); err != nil {
		return Task{}, TaskAttempt{}, fmt.Errorf("提交重试任务事务失败：%w", err)
	}
	return task, attempt, nil
}

func retryableTaskStatus(item Task) bool {
	if item.Status == "failed" || item.Status == "cancelled" {
		return item.TaskType == "certificate_issue" || item.TaskType == "certificate_deploy"
	}
	return item.TaskType == "certificate_deploy" && item.Status == "partial_success"
}
