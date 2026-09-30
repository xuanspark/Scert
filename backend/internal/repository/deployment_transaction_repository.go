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
)

// SQLDeploymentTransactionRepository 持久化远程部署的可恢复现场。
type SQLDeploymentTransactionRepository struct {
	db     *sql.DB
	driver string
}

func NewSQLDeploymentTransactionRepository(db *sql.DB, driver ...string) *SQLDeploymentTransactionRepository {
	return &SQLDeploymentTransactionRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLDeploymentTransactionRepository) query(statement string) string {
	return Rebind(r.driver, statement)
}

func (r *SQLDeploymentTransactionRepository) CreateDeploymentTransaction(ctx context.Context, item deployment.TransactionRecord) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	if err := validateDeploymentTransaction(item); err != nil {
		return err
	}
	values, err := encodeDeploymentTransaction(item)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, r.query(`INSERT INTO deployment_transactions
		(id, task_id, attempt_id, attempt_number, result_index, server_id, server_name,
		 certificate_path, certificate_chain_path, private_key_path, temporary_paths_json,
		 installed_paths_json, backups_json, lock_paths_json, reload_attempted, phase, status,
		 message, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		item.ID, item.TaskID, item.AttemptID, item.AttemptNumber, item.ResultIndex, item.ServerID, item.ServerName,
		values.paths[0], values.paths[1], values.paths[2], values.temporary, values.installed, values.backups,
		values.locks, boolInt(item.ReloadAttempted), item.Phase, item.Status, item.Message, values.updatedAt)
	if err != nil {
		return fmt.Errorf("保存部署事务现场失败：%w", err)
	}
	return nil
}

func (r *SQLDeploymentTransactionRepository) UpdateDeploymentTransaction(ctx context.Context, item deployment.TransactionRecord) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	if err := validateDeploymentTransaction(item); err != nil {
		return err
	}
	values, err := encodeDeploymentTransaction(item)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, r.query(`UPDATE deployment_transactions SET
		certificate_path = ?, certificate_chain_path = ?, private_key_path = ?,
		temporary_paths_json = ?, installed_paths_json = ?, backups_json = ?, lock_paths_json = ?,
		reload_attempted = ?, phase = ?, status = ?, message = ?, updated_at = ? WHERE id = ?`),
		values.paths[0], values.paths[1], values.paths[2], values.temporary, values.installed, values.backups,
		values.locks, boolInt(item.ReloadAttempted), item.Phase, item.Status, item.Message, values.updatedAt, item.ID)
	if err != nil {
		return fmt.Errorf("更新部署事务现场失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取部署事务更新结果失败：%w", err)
	}
	if count != 1 {
		return errors.New("部署事务现场不存在")
	}
	return nil
}

func (r *SQLDeploymentTransactionRepository) FinishDeploymentTransaction(ctx context.Context, id, status, message string) error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if id == "" || status == "" {
		return errors.New("部署事务状态参数不能为空")
	}
	result, err := r.db.ExecContext(ctx, r.query(`UPDATE deployment_transactions SET status = ?, phase = ?, message = ?, updated_at = ? WHERE id = ?`),
		status, status, message, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("完成部署事务记录失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取部署事务完成结果失败：%w", err)
	}
	if count != 1 {
		return errors.New("部署事务现场不存在")
	}
	return nil
}

func (r *SQLDeploymentTransactionRepository) ListPendingDeploymentTransactions(ctx context.Context) ([]deployment.TransactionRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("数据库连接不能为空")
	}
	rows, err := r.db.QueryContext(ctx, r.query(`SELECT id, task_id, attempt_id, attempt_number, result_index,
		server_id, server_name, certificate_path, certificate_chain_path, private_key_path,
		temporary_paths_json, installed_paths_json, backups_json, lock_paths_json,
		reload_attempted, phase, status, message, updated_at
		FROM deployment_transactions WHERE status IN ('pending', 'recovery_required') ORDER BY updated_at, id`))
	if err != nil {
		return nil, fmt.Errorf("查询待恢复部署事务失败：%w", err)
	}
	defer rows.Close()
	items := make([]deployment.TransactionRecord, 0)
	for rows.Next() {
		item, err := scanDeploymentTransaction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取待恢复部署事务失败：%w", err)
	}
	return items, nil
}

type deploymentTransactionValues struct {
	paths     [3]string
	temporary string
	installed string
	backups   string
	locks     string
	updatedAt time.Time
}

func validateDeploymentTransaction(item deployment.TransactionRecord) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.TaskID) == "" || strings.TrimSpace(item.AttemptID) == "" {
		return errors.New("部署事务标识不能为空")
	}
	if item.AttemptNumber < 1 || item.ResultIndex < 0 {
		return errors.New("部署事务序号无效")
	}
	if strings.TrimSpace(item.ServerID) == "" || len(item.Paths) != 3 {
		return errors.New("部署事务服务器或目标路径无效")
	}
	if strings.TrimSpace(item.Phase) == "" || strings.TrimSpace(item.Status) == "" {
		return errors.New("部署事务阶段不能为空")
	}
	return nil
}

func encodeDeploymentTransaction(item deployment.TransactionRecord) (deploymentTransactionValues, error) {
	paths := [3]string{strings.TrimSpace(item.Paths[0]), strings.TrimSpace(item.Paths[1]), strings.TrimSpace(item.Paths[2])}
	temporary, err := json.Marshal(item.TemporaryPaths)
	if err != nil {
		return deploymentTransactionValues{}, fmt.Errorf("编码部署临时路径失败：%w", err)
	}
	installed, err := json.Marshal(item.InstalledPaths)
	if err != nil {
		return deploymentTransactionValues{}, fmt.Errorf("编码部署已安装路径失败：%w", err)
	}
	backups, err := json.Marshal(item.Backups)
	if err != nil {
		return deploymentTransactionValues{}, fmt.Errorf("编码部署备份路径失败：%w", err)
	}
	locks, err := json.Marshal(item.LockPaths)
	if err != nil {
		return deploymentTransactionValues{}, fmt.Errorf("编码部署锁路径失败：%w", err)
	}
	updatedAt := item.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	} else {
		updatedAt = updatedAt.UTC()
	}
	return deploymentTransactionValues{
		paths: paths, temporary: string(temporary), installed: string(installed), backups: string(backups), locks: string(locks), updatedAt: updatedAt,
	}, nil
}

type deploymentTransactionScanner interface {
	Scan(...any) error
}

func scanDeploymentTransaction(row deploymentTransactionScanner) (deployment.TransactionRecord, error) {
	var item deployment.TransactionRecord
	var paths [3]string
	var temporary, installed, backups, locks string
	var reloadAttempted int
	if err := row.Scan(&item.ID, &item.TaskID, &item.AttemptID, &item.AttemptNumber, &item.ResultIndex,
		&item.ServerID, &item.ServerName, &paths[0], &paths[1], &paths[2], &temporary, &installed, &backups, &locks,
		&reloadAttempted, &item.Phase, &item.Status, &item.Message, &item.UpdatedAt); err != nil {
		return deployment.TransactionRecord{}, fmt.Errorf("读取部署事务失败：%w", err)
	}
	item.Paths = paths[:]
	item.ReloadAttempted = reloadAttempted != 0
	if err := json.Unmarshal([]byte(temporary), &item.TemporaryPaths); err != nil {
		return deployment.TransactionRecord{}, fmt.Errorf("解析部署临时路径失败：%w", err)
	}
	if err := json.Unmarshal([]byte(installed), &item.InstalledPaths); err != nil {
		return deployment.TransactionRecord{}, fmt.Errorf("解析部署已安装路径失败：%w", err)
	}
	if err := json.Unmarshal([]byte(backups), &item.Backups); err != nil {
		return deployment.TransactionRecord{}, fmt.Errorf("解析部署备份路径失败：%w", err)
	}
	if err := json.Unmarshal([]byte(locks), &item.LockPaths); err != nil {
		return deployment.TransactionRecord{}, fmt.Errorf("解析部署锁路径失败：%w", err)
	}
	item.UpdatedAt = item.UpdatedAt.UTC()
	return item, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
