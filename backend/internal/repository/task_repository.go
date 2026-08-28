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

type TaskRepository interface {
	CreateTask(context.Context, Task) error
	GetTask(context.Context, string) (Task, error)
	ListTasks(context.Context, string, int) ([]Task, error)
	UpdateTask(context.Context, string, string, string, string, *time.Time, *time.Time) error
	FindActiveTaskByDomain(context.Context, string, string) (Task, error)
	CreateAttempt(context.Context, TaskAttempt) error
	FinishAttempt(context.Context, string, string, string, *time.Time) error
	ListAttempts(context.Context, string) ([]TaskAttempt, error)
}

type SQLTaskRepository struct {
	db *sql.DB
}

func NewSQLTaskRepository(db *sql.DB) *SQLTaskRepository {
	return &SQLTaskRepository{db: db}
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO tasks
		(id, domain_id, task_type, status, stage, message, operator_id, certificate_version_id, idempotency_key, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.DomainID, item.TaskType, item.Status, item.Stage, item.Message, item.OperatorID,
		item.CertificateVersionID, item.IdempotencyKey, nullableTime(item.StartedAt), nullableTime(item.FinishedAt), now, updated)
	if err != nil {
		return fmt.Errorf("创建任务失败：%w", err)
	}
	return nil
}

func (r *SQLTaskRepository) GetTask(ctx context.Context, id string) (Task, error) {
	if r == nil || r.db == nil {
		return Task{}, errors.New("数据库连接不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, errors.New("任务 ID 不能为空")
	}
	return scanTask(r.db.QueryRowContext(ctx, taskSelect+` WHERE id = ?`, id))
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
	rows, err := r.db.QueryContext(ctx, query, args...)
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
	result, err := r.db.ExecContext(ctx, `UPDATE tasks SET status = ?, stage = ?, message = ?, started_at = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
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

func (r *SQLTaskRepository) FindActiveTaskByDomain(ctx context.Context, domainID, taskType string) (Task, error) {
	if r == nil || r.db == nil {
		return Task{}, errors.New("数据库连接不能为空")
	}
	domainID = strings.TrimSpace(domainID)
	taskType = strings.TrimSpace(taskType)
	if domainID == "" || taskType == "" {
		return Task{}, errors.New("域名 ID 和任务类型不能为空")
	}
	return scanTask(r.db.QueryRowContext(ctx, taskSelect+` WHERE domain_id = ? AND task_type = ? AND status IN ('queued', 'running') ORDER BY created_at DESC LIMIT 1`, domainID, taskType))
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO task_attempts
		(id, task_id, attempt_number, stage, status, message, started_at, finished_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.TaskID, item.AttemptNumber, item.Stage, item.Status, item.Message,
		nullableTime(item.StartedAt), nullableTime(item.FinishedAt), item.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("创建任务尝试失败：%w", err)
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
	result, err := r.db.ExecContext(ctx, `UPDATE task_attempts SET status = ?, message = ?, finished_at = ? WHERE id = ?`, status, message, nullableTime(finishedAt), id)
	if err != nil {
		return fmt.Errorf("完成任务尝试失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取任务尝试更新结果失败：%w", err)
	}
	if count == 0 {
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
	rows, err := r.db.QueryContext(ctx, `SELECT id, task_id, attempt_number, stage, status, message, started_at, finished_at, created_at FROM task_attempts WHERE task_id = ? ORDER BY attempt_number`, taskID)
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

const taskSelect = `SELECT id, domain_id, task_type, status, stage, message, operator_id, certificate_version_id, idempotency_key, started_at, finished_at, created_at, updated_at FROM tasks`

func scanTask(row rowScanner) (Task, error) {
	var item Task
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.DomainID, &item.TaskType, &item.Status, &item.Stage, &item.Message, &item.OperatorID, &item.CertificateVersionID, &item.IdempotencyKey, &startedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
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
	if err := row.Scan(&item.ID, &item.TaskID, &item.AttemptNumber, &item.Stage, &item.Status, &item.Message, &startedAt, &finishedAt, &item.CreatedAt); err != nil {
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
	case "queued", "running", "success", "failed", "cancelled":
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
