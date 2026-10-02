package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// TaskDeploymentReport 保存一次尝试的完整公开报告，不包含远端输出或私钥。
type TaskDeploymentReport struct {
	TaskID        string            `json:"task_id"`
	AttemptID     string            `json:"attempt_id"`
	AttemptNumber int               `json:"attempt_number"`
	Report        deployment.Report `json:"report"`
	CreatedAt     time.Time         `json:"created_at"`
}

func publicTaskReport(report deployment.Report) deployment.Report {
	report.Results = append([]deployment.Result{}, report.Results...)
	for index := range report.Results {
		report.Results[index] = deployment.PublicResult(report.Results[index])
	}
	return report
}

// 报告、逐目标结果和两条终态共用事务；任一保存失败均保留活动记录供恢复接管。
func (r *SQLTaskRepository) recordCompletionReport(ctx context.Context, tx *sql.Tx, completion TaskCompletion) error {
	var attemptNumber int
	if err := tx.QueryRowContext(ctx, r.query(`SELECT attempt_number FROM task_attempts WHERE id = ? AND task_id = ?`),
		completion.AttemptID, completion.TaskID).Scan(&attemptNumber); err != nil {
		return fmt.Errorf("读取报告所属尝试失败：%w", err)
	}
	report := publicTaskReport(*completion.Report)
	content, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("序列化部署报告失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, r.query(`INSERT INTO task_deployment_reports
		(task_id, attempt_id, attempt_number, report_json, created_at) VALUES (?, ?, ?, ?, ?)`),
		completion.TaskID, completion.AttemptID, attemptNumber, string(content), completion.FinishedAt.UTC()); err != nil {
		return fmt.Errorf("保存完整部署报告失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, r.query(`DELETE FROM task_deployment_results WHERE task_id = ? AND attempt_id = ?`),
		completion.TaskID, completion.AttemptID); err != nil {
		return fmt.Errorf("清理尝试旧结果失败：%w", err)
	}
	for index, result := range report.Results {
		if _, err := tx.ExecContext(ctx, r.query(`INSERT INTO task_deployment_results
			(task_id, attempt_id, attempt_number, result_index, server_id, server_name, status, message, backup_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), completion.TaskID, completion.AttemptID, attemptNumber,
			index, result.ServerID, result.Server, result.Status, result.Message, result.BackupID, completion.FinishedAt.UTC()); err != nil {
			return fmt.Errorf("保存报告逐服务器结果失败：%w", err)
		}
	}
	return nil
}

func (r *SQLTaskRepository) ListTaskDeploymentReports(ctx context.Context, taskID string) ([]TaskDeploymentReport, error) {
	if r == nil || r.db == nil || strings.TrimSpace(taskID) == "" {
		return nil, errors.New("数据库连接和任务编号不能为空")
	}
	return r.listTaskReports(ctx, `SELECT task_id, attempt_id, attempt_number, report_json, created_at
		FROM task_deployment_reports WHERE task_id = ? ORDER BY attempt_number`, taskID)
}

func (r *SQLTaskRepository) ListVisibleTaskDeploymentReports(ctx context.Context, actorID, taskID string) ([]TaskDeploymentReport, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	predicate, args := taskVisibilityPredicate("tasks", domain.PermissionTaskRead)
	args[0] = strings.TrimSpace(actorID)
	args = append(args, strings.TrimSpace(taskID))
	return r.listTaskReports(ctx, `SELECT reports.task_id, reports.attempt_id, reports.attempt_number, reports.report_json, reports.created_at
		FROM task_deployment_reports reports JOIN tasks ON tasks.id = reports.task_id
		WHERE `+predicate+` AND reports.task_id = ? ORDER BY reports.attempt_number`, args...)
}

func (r *SQLTaskRepository) listTaskReports(ctx context.Context, query string, args ...any) ([]TaskDeploymentReport, error) {
	rows, err := r.db.QueryContext(ctx, r.query(query), args...)
	if err != nil {
		return nil, fmt.Errorf("查询完整部署报告失败：%w", err)
	}
	defer rows.Close()
	items := make([]TaskDeploymentReport, 0)
	for rows.Next() {
		var item TaskDeploymentReport
		var content string
		if err := rows.Scan(&item.TaskID, &item.AttemptID, &item.AttemptNumber, &content, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取完整部署报告失败：%w", err)
		}
		if err := json.Unmarshal([]byte(content), &item.Report); err != nil {
			return nil, fmt.Errorf("解析完整部署报告失败：%w", err)
		}
		item.Report = publicTaskReport(item.Report)
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取完整部署报告列表失败：%w", err)
	}
	return items, nil
}
