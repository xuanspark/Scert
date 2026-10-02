package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// TaskValidationResult 仅保存已校验的公开元数据，不包含证书路径和私钥。
type TaskValidationResult struct {
	TaskID        string                       `json:"task_id"`
	AttemptID     string                       `json:"attempt_id"`
	AttemptNumber int                          `json:"attempt_number"`
	Validation    certificate.ValidationResult `json:"validation"`
	CreatedAt     time.Time                    `json:"created_at"`
}

type TaskValidationReader interface {
	ListVisibleTaskValidations(context.Context, string, string) ([]TaskValidationResult, error)
}

func (r *SQLTaskRepository) recordCompletionValidation(ctx context.Context, tx *sql.Tx, completion TaskCompletion) error {
	if completion.Report != nil || completion.Status != "success" || completion.Retry {
		return errors.New("校验元数据只能随独立校验任务成功提交")
	}
	var attemptNumber int
	var taskType string
	if err := tx.QueryRowContext(ctx, r.query(`SELECT attempts.attempt_number, tasks.task_type
		FROM task_attempts attempts JOIN tasks ON tasks.id = attempts.task_id
		WHERE attempts.id = ? AND tasks.id = ?`), completion.AttemptID, completion.TaskID).Scan(&attemptNumber, &taskType); err != nil {
		return fmt.Errorf("读取校验结果所属任务失败：%w", err)
	}
	if taskType != "certificate_validate" {
		return errors.New("校验结果不能保存到其他类型任务")
	}
	content, err := json.Marshal(completion.Validation)
	if err != nil {
		return fmt.Errorf("序列化校验结果失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, r.query(`INSERT INTO task_validation_results
		(task_id, attempt_id, attempt_number, validation_json, created_at) VALUES (?, ?, ?, ?, ?)`),
		completion.TaskID, completion.AttemptID, attemptNumber, string(content), completion.FinishedAt.UTC()); err != nil {
		return fmt.Errorf("保存校验结果失败：%w", err)
	}
	return nil
}

func (r *SQLTaskRepository) ListVisibleTaskValidations(ctx context.Context, actorID, taskID string) ([]TaskValidationResult, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	predicate, args := taskVisibilityPredicate("tasks", domain.PermissionTaskRead)
	args[0] = strings.TrimSpace(actorID)
	args = append(args, strings.TrimSpace(taskID))
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT results.task_id, results.attempt_id, results.attempt_number,
		results.validation_json, results.created_at FROM task_validation_results results
		JOIN tasks ON tasks.id = results.task_id WHERE `+predicate+` AND results.task_id = ? ORDER BY results.attempt_number`), args...)
	if err != nil {
		return nil, fmt.Errorf("查询任务校验结果失败：%w", err)
	}
	defer rows.Close()
	items := make([]TaskValidationResult, 0)
	for rows.Next() {
		var item TaskValidationResult
		var content string
		if err := rows.Scan(&item.TaskID, &item.AttemptID, &item.AttemptNumber, &content, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取任务校验结果失败：%w", err)
		}
		if err := json.Unmarshal([]byte(content), &item.Validation); err != nil {
			return nil, fmt.Errorf("解析任务校验结果失败：%w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取任务校验结果列表失败：%w", err)
	}
	return items, nil
}
