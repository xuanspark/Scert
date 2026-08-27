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

type ServerRepository interface {
	CreateServer(ctx context.Context, server deployment.Server) error
	UpdateServer(ctx context.Context, server deployment.Server) error
	GetServerByID(ctx context.Context, id string) (deployment.Server, error)
	ListServers(ctx context.Context) ([]deployment.Server, error)
	DeleteServer(ctx context.Context, id string) error
	IsPrivateKeyRefUsed(ctx context.Context, reference string) (bool, error)
	ListServersByDomainID(ctx context.Context, domainID string) ([]deployment.Server, error)
	ReplaceDomainServers(ctx context.Context, domainID string, serverIDs []string) error
}

type SQLServerRepository struct {
	db *sql.DB
}

func NewSQLServerRepository(db *sql.DB) *SQLServerRepository {
	return &SQLServerRepository{db: db}
}

func (r *SQLServerRepository) CreateServer(ctx context.Context, server deployment.Server) error {
	server = server.Normalize()
	if err := validateServer(server); err != nil {
		return err
	}
	command, err := json.Marshal(server.ReloadCommand)
	if err != nil {
		return fmt.Errorf("编码 reload 命令失败：%w", err)
	}
	createdAt := server.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := server.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO servers
		(id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, server.ID, server.Name, server.Address, server.Port, server.Username, server.PrivateKeyRef,
		server.HostKeyFingerprint, server.CertificatePath, server.PrivateKeyPath, server.CertificateChainPath, server.WebServer, string(command), createdAt, updatedAt)
	if err != nil {
		return fmt.Errorf("保存服务器失败：%w", err)
	}
	return nil
}

func (r *SQLServerRepository) UpdateServer(ctx context.Context, server deployment.Server) error {
	server = server.Normalize()
	if err := validateServer(server); err != nil {
		return err
	}
	command, err := json.Marshal(server.ReloadCommand)
	if err != nil {
		return fmt.Errorf("编码 reload 命令失败：%w", err)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE servers SET name = ?, address = ?, port = ?, username = ?, private_key_ref = ?, host_key_fingerprint = ?, certificate_path = ?, private_key_path = ?, certificate_chain_path = ?, web_server = ?, reload_command_json = ?, updated_at = ? WHERE id = ?`,
		server.Name, server.Address, server.Port, server.Username, server.PrivateKeyRef, server.HostKeyFingerprint, server.CertificatePath, server.PrivateKeyPath, server.CertificateChainPath, server.WebServer, string(command), time.Now().UTC(), server.ID)
	if err != nil {
		return fmt.Errorf("更新服务器失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取服务器更新结果失败：%w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLServerRepository) GetServerByID(ctx context.Context, id string) (deployment.Server, error) {
	row := r.db.QueryRowContext(ctx, serverSelect+` WHERE id = ?`, strings.TrimSpace(id))
	return scanServer(row)
}

func (r *SQLServerRepository) ListServers(ctx context.Context) ([]deployment.Server, error) {
	rows, err := r.db.QueryContext(ctx, serverSelect+` ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("查询服务器失败：%w", err)
	}
	defer rows.Close()
	result := make([]deployment.Server, 0)
	for rows.Next() {
		server, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, server)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取服务器失败：%w", err)
	}
	return result, nil
}

func (r *SQLServerRepository) DeleteServer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("服务器 ID 不能为空")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始删除服务器失败：%w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM domain_servers WHERE server_id = ?", id); err != nil {
		return rollback(fmt.Errorf("删除服务器关联失败：%w", err))
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		return rollback(fmt.Errorf("删除服务器失败：%w", err))
	}
	count, err := result.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("读取服务器删除结果失败：%w", err))
	}
	if count == 0 {
		return rollback(sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交服务器删除失败：%w", err)
	}
	return nil
}

func (r *SQLServerRepository) IsPrivateKeyRefUsed(ctx context.Context, reference string) (bool, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return false, errors.New("SSH 私钥引用不能为空")
	}
	var count int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM servers WHERE private_key_ref = ?", reference).Scan(&count); err != nil {
		return false, fmt.Errorf("检查 SSH 私钥引用占用状态失败：%w", err)
	}
	return count > 0, nil
}

func (r *SQLServerRepository) ListServersByDomainID(ctx context.Context, domainID string) ([]deployment.Server, error) {
	rows, err := r.db.QueryContext(ctx, serverSelectQualified+` INNER JOIN domain_servers ds ON ds.server_id = servers.id WHERE ds.domain_id = ? ORDER BY servers.name, servers.id`, strings.TrimSpace(domainID))
	if err != nil {
		return nil, fmt.Errorf("查询域名部署服务器失败：%w", err)
	}
	defer rows.Close()
	result := make([]deployment.Server, 0)
	for rows.Next() {
		server, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, server)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取域名部署服务器失败：%w", err)
	}
	return result, nil
}

func (r *SQLServerRepository) ReplaceDomainServers(ctx context.Context, domainID string, serverIDs []string) error {
	if strings.TrimSpace(domainID) == "" {
		return errors.New("域名 ID 不能为空")
	}
	cleaned := make([]string, 0, len(serverIDs))
	seen := make(map[string]struct{}, len(serverIDs))
	for _, id := range serverIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return errors.New("服务器 ID 不能为空")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始更新域名服务器关联失败：%w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM domain_servers WHERE domain_id = ?", domainID); err != nil {
		return rollback(fmt.Errorf("清理域名服务器关联失败：%w", err))
	}
	for _, serverID := range cleaned {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM servers WHERE id = ?", serverID).Scan(&exists); err != nil {
			return rollback(fmt.Errorf("校验部署服务器失败：%w", err))
		}
		if exists == 0 {
			return rollback(fmt.Errorf("部署服务器不存在：%s", serverID))
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO domain_servers (domain_id, server_id) VALUES (?, ?)", domainID, serverID); err != nil {
			return rollback(fmt.Errorf("保存域名服务器关联失败：%w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交域名服务器关联失败：%w", err)
	}
	return nil
}

const serverSelect = `SELECT id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json, created_at, updated_at FROM servers`
const serverSelectQualified = `SELECT servers.id, servers.name, servers.address, servers.port, servers.username, servers.private_key_ref, servers.host_key_fingerprint, servers.certificate_path, servers.private_key_path, servers.certificate_chain_path, servers.web_server, servers.reload_command_json, servers.created_at, servers.updated_at FROM servers`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (deployment.Server, error) {
	var server deployment.Server
	var webServer string
	var commandJSON string
	if err := row.Scan(&server.ID, &server.Name, &server.Address, &server.Port, &server.Username, &server.PrivateKeyRef, &server.HostKeyFingerprint, &server.CertificatePath, &server.PrivateKeyPath, &server.CertificateChainPath, &webServer, &commandJSON, &server.CreatedAt, &server.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deployment.Server{}, sql.ErrNoRows
		}
		return deployment.Server{}, fmt.Errorf("读取服务器失败：%w", err)
	}
	server.WebServer = deployment.WebServer(webServer)
	if err := json.Unmarshal([]byte(commandJSON), &server.ReloadCommand); err != nil {
		return deployment.Server{}, fmt.Errorf("解析 reload 命令失败：%w", err)
	}
	return server.Normalize(), nil
}

func validateServer(server deployment.Server) error {
	if err := server.Validate(); err != nil {
		return err
	}
	return nil
}
