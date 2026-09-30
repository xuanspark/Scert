package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task 表示一项可持久化的证书或部署任务。
type Task struct {
	HistoryScope
	ID                   string     `json:"id"`
	DomainID             string     `json:"domain_id"`
	TaskType             string     `json:"task_type"`
	Status               string     `json:"status"`
	Stage                string     `json:"stage"`
	Message              string     `json:"message"`
	OperatorID           string     `json:"operator_id,omitempty"`
	CertificateVersionID string     `json:"certificate_version_id,omitempty"`
	IdempotencyKey       string     `json:"idempotency_key,omitempty"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	FinishedAt           *time.Time `json:"finished_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// TaskAttempt 表示任务的一次执行尝试。
type TaskAttempt struct {
	HistoryScope
	ID            string     `json:"id"`
	TaskID        string     `json:"task_id"`
	AttemptNumber int        `json:"attempt_number"`
	Stage         string     `json:"stage"`
	Status        string     `json:"status"`
	Message       string     `json:"message"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type TaskDeploymentResult struct {
	TaskID        string    `json:"task_id"`
	AttemptID     string    `json:"attempt_id"`
	AttemptNumber int       `json:"attempt_number"`
	ResultIndex   int       `json:"result_index"`
	ServerID      string    `json:"server_id"`
	Server        string    `json:"server"`
	Status        string    `json:"status"`
	Message       string    `json:"message"`
	BackupID      string    `json:"backup_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ErrTaskAlreadyRunning 表示同一域名的同类任务仍处于活动状态。
var ErrTaskAlreadyRunning = errors.New("同一域名的同类任务正在执行")

type TaskRepository interface {
	CreateTask(context.Context, Task) error
	GetTask(context.Context, string) (Task, error)
	ListTasks(context.Context, string, int) ([]Task, error)
	UpdateTask(context.Context, string, string, string, string, *time.Time, *time.Time) error
	CancelTask(context.Context, string, string, *time.Time) error
	RecoverInterruptedTasks(context.Context, time.Time) (int, error)
	FindActiveTaskByDomain(context.Context, string, string) (Task, error)
	FindTaskByIdempotencyKey(context.Context, string, string) (Task, error)
	CreateAttempt(context.Context, TaskAttempt) error
	FinishAttempt(context.Context, string, string, string, *time.Time) error
	ListAttempts(context.Context, string) ([]TaskAttempt, error)
	RecordTaskDeploymentResults(context.Context, string, string, int, []TaskDeploymentResult) error
	ListTaskDeploymentResults(context.Context, string) ([]TaskDeploymentResult, error)
}

type SQLTaskRepository struct {
	db     *sql.DB
	driver string
}

func NewSQLTaskRepository(db *sql.DB, driver ...string) *SQLTaskRepository {
	return &SQLTaskRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLTaskRepository) query(statement string) string {
	return Rebind(r.driver, statement)
}

func (r *SQLTaskRepository) CreateTask(ctx context.Context, item Task) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	item = normalizeTask(item)
	if err := validateTask(item); err != nil {
		return err
	}
	now := item.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	updated := item.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	args := []any{
		item.ID, item.DomainID, item.TaskType, item.Status, item.Stage, item.Message, item.OperatorID,
		item.CertificateVersionID, item.IdempotencyKey, nullableTime(item.StartedAt), nullableTime(item.FinishedAt), now, updated,
	}
	insertQuery := `INSERT INTO tasks
		(id, domain_id, task_type, status, stage, message, operator_id, certificate_version_id, idempotency_key, started_at, finished_at, created_at, updated_at, scope_type, project_id) `
	if item.TaskType == "management_renewal" {
		if item.DomainID != ManagementResourceID || !item.HistoryScope.matches(HistoryScope{ScopeType: ScopeSystem}) {
			return errors.New("管理证书任务范围无效")
		}
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("开始管理证书任务事务失败：%w", err)
		}
		defer tx.Rollback()
		var activeTaskID string
		if err := tx.QueryRowContext(ctx, r.query(`SELECT id FROM tasks
			WHERE domain_id = ? AND task_type = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')
			ORDER BY created_at DESC LIMIT 1`), item.DomainID, item.TaskType).Scan(&activeTaskID); err == nil {
			return fmt.Errorf("%w：%s", ErrTaskAlreadyRunning, activeTaskID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("检查管理证书活动任务失败：%w", err)
		}
		result, err := tx.ExecContext(ctx, r.query(insertQuery+`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'system', '')`), args...)
		if err != nil {
			return fmt.Errorf("创建任务失败：%w", err)
		}
		if err := validateTaskInsertResult(result); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交管理证书任务事务失败：%w", err)
		}
		return nil
	}
	if item.DomainID == ManagementResourceID || (item.ScopeType != "" && item.ScopeType != ScopeProject) {
		return errors.New("业务任务必须归属于实际域名项目")
	}

	// 创建任务与删除域名使用相同的域名行锁，避免删除检查完成后又插入在途任务。
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始任务创建事务失败：%w", err)
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRowContext(ctx, r.query(`SELECT d.project_id FROM domains d
		JOIN projects p ON p.id = d.project_id WHERE d.id = ? AND (? = '' OR d.project_id = ?)`+taskResourceWriteLock(r.driver)),
		item.DomainID, item.ProjectID, item.ProjectID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("任务资源不存在或项目不匹配")
		}
		return fmt.Errorf("读取任务所属域名失败：%w", err)
	}
	var activeTaskID string
	if err := tx.QueryRowContext(ctx, r.query(`SELECT id FROM tasks
		WHERE domain_id = ? AND task_type = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')
		ORDER BY created_at DESC LIMIT 1`), item.DomainID, item.TaskType).Scan(&activeTaskID); err == nil {
		return fmt.Errorf("%w：%s", ErrTaskAlreadyRunning, activeTaskID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("检查活动任务失败：%w", err)
	}
	args = append(args, projectID)
	result, err := tx.ExecContext(ctx, r.query(insertQuery+`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'project', ?)`), args...)
	if err != nil {
		return fmt.Errorf("创建任务失败：%w", err)
	}
	if err := validateTaskInsertResult(result); err != nil {
		return err
	}
	if item.IdempotencyKey != "" {
		idempotencyResult, idempotencyErr := tx.ExecContext(ctx, r.query(`INSERT INTO task_idempotency
			(domain_id, idempotency_key, task_id, created_at) VALUES (?, ?, ?, ?)`),
			item.DomainID, item.IdempotencyKey, item.ID, now)
		if idempotencyErr != nil {
			return fmt.Errorf("创建任务幂等记录失败：%w", idempotencyErr)
		}
		if err := validateTaskInsertResult(idempotencyResult); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交任务创建事务失败：%w", err)
	}
	return nil
}

func validateTaskInsertResult(result sql.Result) error {
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("读取任务创建结果失败：%w", err)
	} else if count != 1 {
		return errors.New("任务资源不存在或项目不匹配")
	}
	return nil
}

func taskResourceWriteLock(driver string) string {
	switch driver {
	case "mysql", "postgres":
		return " FOR UPDATE"
	default:
		return ""
	}
}

func (r *SQLTaskRepository) GetTask(ctx context.Context, id string) (Task, error) {
	if r == nil || r.db == nil {
		return Task{}, errors.New("数据库连接不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, errors.New("任务 ID 不能为空")
	}
	return scanTask(r.db.QueryRowContext(ctx, r.query(taskSelect+` WHERE id = ?`), id))
}

func (r *SQLTaskRepository) ListTasks(ctx context.Context, domainID string, limit int) ([]Task, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := taskSelect
	args := make([]any, 0, 1)
	if domainID = strings.TrimSpace(domainID); domainID != "" {
		query += " WHERE domain_id = ?"
		args = append(args, domainID)
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)
	rows, err := r.db.QueryContext(ctx, r.query(query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表失败：%w", err)
	}
	defer rows.Close()
	result := make([]Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取任务列表失败：%w", err)
	}
	return result, nil
}

func (r *SQLTaskRepository) UpdateTask(ctx context.Context, id, status, stage, message string, startedAt, finishedAt *time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	stage = strings.TrimSpace(stage)
	if id == "" {
		return errors.New("任务 ID 不能为空")
	}
	if !validTaskStatus(status) || stage == "" {
		return errors.New("任务状态或阶段无效")
	}
	result, err := r.db.ExecContext(ctx, r.query(`UPDATE tasks SET status = ?, stage = ?, message = ?, started_at = ?, finished_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')`),
		status, stage, message, nullableTime(startedAt), nullableTime(finishedAt), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("更新任务失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取任务更新结果失败：%w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLTaskRepository) CancelTask(ctx context.Context, id, message string, finishedAt *time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("任务 ID 不能为空")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始取消任务事务失败：%w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	result, err := tx.ExecContext(ctx, r.query(`UPDATE tasks SET status = 'cancelled', message = ?, finished_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')`), message, nullableTime(finishedAt), time.Now().UTC(), id)
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
		item, getErr := scanTask(tx.QueryRowContext(ctx, r.query(taskSelect+` WHERE id = ?`), id))
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
		return errors.New("任务当前无法取消")
	}
	if _, err := tx.ExecContext(ctx, r.query(`UPDATE task_attempts SET status = 'cancelled', message = ?, finished_at = ? WHERE task_id = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling')`), message, nullableTime(finishedAt), id); err != nil {
		rollback()
		return fmt.Errorf("取消任务尝试失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交取消任务事务失败：%w", err)
	}
	return nil
}

// RecoverInterruptedTasks 将服务重启前未完成的任务标记为失败，避免任务永久停留在执行状态。
func (r *SQLTaskRepository) RecoverInterruptedTasks(ctx context.Context, finishedAt time.Time) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("\u6570\u636e\u5e93\u8fde\u63a5\u4e0d\u80fd\u4e3a\u7a7a")
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	} else {
		finishedAt = finishedAt.UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("\u5f00\u59cb\u6062\u590d\u4e2d\u65ad\u4efb\u52a1\u4e8b\u52a1\u5931\u8d25\uff1a%w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	recoveryMessage := "\u670d\u52a1\u91cd\u542f\u5bfc\u81f4\u4efb\u52a1\u4e2d\u65ad\uff0c\u8bf7\u91cd\u65b0\u6267\u884c"
	auditResult, err := tx.ExecContext(ctx, r.query(`INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		SELECT 'task_recovery', 'task', id, 'failed', ?, ?, scope_type, project_id, id, operator_id
		FROM tasks
		WHERE status IN ('queued', 'running', 'retry_wait', 'cancelling')`), recoveryMessage, finishedAt)
	if err != nil {
		rollback()
		return 0, fmt.Errorf("\u5199\u5165\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u5931\u8d25\uff1a%w", err)
	}
	auditCount, err := auditResult.RowsAffected()
	if err != nil {
		rollback()
		return 0, fmt.Errorf("\u8bfb\u53d6\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u6570\u91cf\u5931\u8d25\uff1a%w", err)
	}
	if _, err := tx.ExecContext(ctx, r.query(`UPDATE task_attempts
		SET status = 'failed', message = ?, finished_at = ?
		WHERE status IN ('queued', 'running', 'retry_wait', 'cancelling')
		AND task_id IN (
			SELECT id FROM tasks
			WHERE status IN ('queued', 'running', 'retry_wait', 'cancelling')
		)`), recoveryMessage, finishedAt); err != nil {
		rollback()
		return 0, fmt.Errorf("恢复中断任务尝试失败：%w", err)
	}
	result, err := tx.ExecContext(ctx, r.query(`UPDATE tasks
		SET status = 'failed', stage = 'service_restart', message = ?, finished_at = ?, updated_at = ?
		WHERE status IN ('queued', 'running', 'retry_wait', 'cancelling')`), recoveryMessage, finishedAt, finishedAt)
	if err != nil {
		rollback()
		return 0, fmt.Errorf("\u6062\u590d\u4e2d\u65ad\u4efb\u52a1\u5931\u8d25\uff1a%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		rollback()
		return 0, fmt.Errorf("\u8bfb\u53d6\u4e2d\u65ad\u4efb\u52a1\u6570\u91cf\u5931\u8d25\uff1a%w", err)
	}
	if auditCount != count {
		rollback()
		return 0, fmt.Errorf("\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u6570\u91cf\u4e0e\u4efb\u52a1\u6570\u91cf\u4e0d\u4e00\u81f4\uff1a\u5ba1\u8ba1 %d，\u4efb\u52a1 %d", auditCount, count)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("\u63d0\u4ea4\u4e2d\u65ad\u4efb\u52a1\u6062\u590d\u4e8b\u52a1\u5931\u8d25\uff1a%w", err)
	}
	return int(count), nil
}

func (r *SQLTaskRepository) FindActiveTaskByDomain(ctx context.Context, domainID, taskType string) (Task, error) {
	if r == nil || r.db == nil {
		return Task{}, errors.New("数据库连接不能为空")
	}
	domainID = strings.TrimSpace(domainID)
	taskType = strings.TrimSpace(taskType)
	if domainID == "" || taskType == "" {
		return Task{}, errors.New("域名 ID 和任务类型不能为空")
	}
	return scanTask(r.db.QueryRowContext(ctx, r.query(taskSelect+` WHERE domain_id = ? AND task_type = ? AND status IN ('queued', 'running', 'retry_wait', 'cancelling') ORDER BY created_at DESC LIMIT 1`), domainID, taskType))
}

func (r *SQLTaskRepository) FindTaskByIdempotencyKey(ctx context.Context, domainID, key string) (Task, error) {
	if r == nil || r.db == nil {
		return Task{}, errors.New("数据库连接不能为空")
	}
	domainID = strings.TrimSpace(domainID)
	key = strings.TrimSpace(key)
	if domainID == "" || key == "" {
		return Task{}, errors.New("域名 ID 和幂等键不能为空")
	}
	return scanTask(r.db.QueryRowContext(ctx, r.query(taskSelect+` WHERE domain_id = ? AND idempotency_key = ? ORDER BY created_at DESC LIMIT 1`), domainID, key))
}

func (r *SQLTaskRepository) CreateAttempt(ctx context.Context, item TaskAttempt) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	item.ID = strings.TrimSpace(item.ID)
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.Stage = strings.TrimSpace(item.Stage)
	item.Status = strings.TrimSpace(item.Status)
	if item.ID == "" || item.TaskID == "" || item.AttemptNumber <= 0 {
		return errors.New("任务尝试必要字段不能为空")
	}
	if item.Stage == "" || !validTaskStatus(item.Status) {
		return errors.New("任务尝试阶段或状态无效")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, r.query(`INSERT INTO task_attempts
		(id, task_id, attempt_number, stage, status, message, started_at, finished_at, created_at)
		SELECT ?, id, ?, ?, ?, ?, ?, ?, ? FROM tasks t WHERE id = ?
		AND (? = '' OR scope_type = ?) AND (? = '' OR project_id = ?)
		AND ((t.scope_type = 'system' AND t.task_type = 'management_renewal' AND t.domain_id = 'management-certificate')
		OR (t.scope_type = 'project' AND EXISTS (SELECT 1 FROM domains d JOIN projects p ON p.id = d.project_id
			WHERE d.id = t.domain_id AND d.project_id = t.project_id)))`), item.ID, item.AttemptNumber, item.Stage, item.Status, item.Message,
		nullableTime(item.StartedAt), nullableTime(item.FinishedAt), item.CreatedAt.UTC(), item.TaskID,
		item.ScopeType, item.ScopeType, item.ProjectID, item.ProjectID)
	if err != nil {
		return fmt.Errorf("创建任务尝试失败：%w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("读取任务尝试创建结果失败：%w", err)
	} else if count != 1 {
		return errors.New("任务尝试的父任务不存在或范围不匹配")
	}
	return nil
}

func (r *SQLTaskRepository) FinishAttempt(ctx context.Context, id, status, message string, finishedAt *time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if id == "" || !validTaskStatus(status) {
		return errors.New("任务尝试 ID 或状态无效")
	}
	result, err := r.db.ExecContext(ctx, r.query(`UPDATE task_attempts SET status = ?, message = ?, finished_at = ? WHERE id = ? AND (status IN ('queued', 'running') OR status = ?)`), status, message, nullableTime(finishedAt), id, status)
	if err != nil {
		return fmt.Errorf("完成任务尝试失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取任务尝试更新结果失败：%w", err)
	}
	if count == 0 {
		var currentStatus string
		if err := r.db.QueryRowContext(ctx, r.query(`SELECT status FROM task_attempts WHERE id = ?`), id).Scan(&currentStatus); err != nil {
			return err
		}
		if currentStatus == "cancelled" {
			return nil
		}
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLTaskRepository) ListAttempts(ctx context.Context, taskID string) ([]TaskAttempt, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("任务 ID 不能为空")
	}
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT a.id, a.task_id, a.attempt_number, a.stage, a.status, a.message,
		a.started_at, a.finished_at, a.created_at, COALESCE(t.scope_type, 'unknown'), COALESCE(t.project_id, '')
		FROM task_attempts a LEFT JOIN tasks t ON t.id = a.task_id WHERE a.task_id = ? ORDER BY a.attempt_number`), taskID)
	if err != nil {
		return nil, fmt.Errorf("查询任务尝试失败：%w", err)
	}
	defer rows.Close()
	result := make([]TaskAttempt, 0)
	for rows.Next() {
		item, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取任务尝试列表失败：%w", err)
	}
	return result, nil
}

func (r *SQLTaskRepository) RecordTaskDeploymentResults(ctx context.Context, taskID, attemptID string, attemptNumber int, items []TaskDeploymentResult) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	taskID = strings.TrimSpace(taskID)
	attemptID = strings.TrimSpace(attemptID)
	if taskID == "" || attemptID == "" || attemptNumber <= 0 {
		return errors.New("任务和任务尝试标识不能为空")
	}
	if len(items) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始保存部署结果事务失败：%w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	var attemptCount int
	if err := tx.QueryRowContext(ctx, r.query(`SELECT COUNT(*) FROM task_attempts WHERE id = ? AND task_id = ? AND attempt_number = ?`), attemptID, taskID, attemptNumber).Scan(&attemptCount); err != nil {
		rollback()
		return fmt.Errorf("校验任务尝试失败：%w", err)
	}
	if attemptCount != 1 {
		rollback()
		return errors.New("任务尝试不存在或不匹配")
	}
	if _, err := tx.ExecContext(ctx, r.query(`DELETE FROM task_deployment_results WHERE task_id = ? AND attempt_id = ?`), taskID, attemptID); err != nil {
		rollback()
		return fmt.Errorf("清理旧部署结果失败：%w", err)
	}
	seen := make(map[int]struct{}, len(items))
	for index, item := range items {
		resultIndex := item.ResultIndex
		if resultIndex < 0 {
			resultIndex = index
		}
		if _, exists := seen[resultIndex]; exists {
			rollback()
			return errors.New("部署结果序号重复")
		}
		seen[resultIndex] = struct{}{}
		createdAt := item.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, r.query(`INSERT INTO task_deployment_results
			(task_id, attempt_id, attempt_number, result_index, server_id, server_name, status, message, backup_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			taskID, attemptID, attemptNumber, resultIndex, strings.TrimSpace(item.ServerID), strings.TrimSpace(item.Server),
			strings.TrimSpace(item.Status), item.Message, strings.TrimSpace(item.BackupID), createdAt.UTC()); err != nil {
			rollback()
			return fmt.Errorf("保存部署结果失败：%w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交部署结果事务失败：%w", err)
	}
	return nil
}

func (r *SQLTaskRepository) ListTaskDeploymentResults(ctx context.Context, taskID string) ([]TaskDeploymentResult, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("任务 ID 不能为空")
	}
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT task_id, attempt_id, attempt_number, result_index,
		server_id, server_name, status, message, backup_id, created_at
		FROM task_deployment_results WHERE task_id = ? ORDER BY attempt_number, result_index`), taskID)
	if err != nil {
		return nil, fmt.Errorf("查询部署结果失败：%w", err)
	}
	defer rows.Close()
	items := make([]TaskDeploymentResult, 0)
	for rows.Next() {
		var item TaskDeploymentResult
		if err := rows.Scan(&item.TaskID, &item.AttemptID, &item.AttemptNumber, &item.ResultIndex,
			&item.ServerID, &item.Server, &item.Status, &item.Message, &item.BackupID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取部署结果失败：%w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取部署结果列表失败：%w", err)
	}
	return items, nil
}

const taskSelect = `SELECT id, domain_id, task_type, status, stage, message, operator_id, certificate_version_id, idempotency_key, started_at, finished_at, created_at, updated_at, scope_type, project_id FROM tasks`

func scanTask(row rowScanner) (Task, error) {
	var item Task
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.DomainID, &item.TaskType, &item.Status, &item.Stage, &item.Message, &item.OperatorID, &item.CertificateVersionID, &item.IdempotencyKey, &startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt, &item.ScopeType, &item.ProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, sql.ErrNoRows
		}
		return Task{}, fmt.Errorf("读取任务失败：%w", err)
	}
	item.StartedAt = nullTime(startedAt)
	item.FinishedAt = nullTime(finishedAt)
	return item, nil
}

func scanAttempt(row rowScanner) (TaskAttempt, error) {
	var item TaskAttempt
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.TaskID, &item.AttemptNumber, &item.Stage, &item.Status, &item.Message, &startedAt, &finishedAt, &item.CreatedAt, &item.ScopeType, &item.ProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskAttempt{}, sql.ErrNoRows
		}
		return TaskAttempt{}, fmt.Errorf("读取任务尝试失败：%w", err)
	}
	item.StartedAt = nullTime(startedAt)
	item.FinishedAt = nullTime(finishedAt)
	return item, nil
}

func normalizeTask(item Task) Task {
	item.ID = strings.TrimSpace(item.ID)
	item.DomainID = strings.TrimSpace(item.DomainID)
	item.TaskType = strings.TrimSpace(item.TaskType)
	item.Status = strings.TrimSpace(item.Status)
	item.Stage = strings.TrimSpace(item.Stage)
	item.OperatorID = strings.TrimSpace(item.OperatorID)
	item.CertificateVersionID = strings.TrimSpace(item.CertificateVersionID)
	item.IdempotencyKey = strings.TrimSpace(item.IdempotencyKey)
	if item.Status == "" {
		item.Status = "queued"
	}
	if item.Stage == "" {
		item.Stage = "queued"
	}
	return item
}

func validateTask(item Task) error {
	if item.ID == "" || item.DomainID == "" || item.TaskType == "" {
		return errors.New("任务 ID、域名 ID 和任务类型不能为空")
	}
	if !validTaskStatus(item.Status) {
		return fmt.Errorf("任务状态无效：%s", item.Status)
	}
	return nil
}

func validTaskStatus(status string) bool {
	switch status {
	case "queued", "running", "success", "failed", "cancelling", "cancelled", "retry_wait", "partial_success":
		return true
	default:
		return false
	}
}

func nullTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

var _ TaskRepository = (*SQLTaskRepository)(nil)
