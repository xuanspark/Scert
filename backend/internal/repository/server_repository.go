package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type ServerRepository interface {
	CreateServer(ctx context.Context, server deployment.Server) error
	UpdateServer(ctx context.Context, server deployment.Server) error
	GetServerByID(ctx context.Context, id string) (deployment.Server, error)
	ListServers(ctx context.Context) ([]deployment.Server, error)
	DeleteServer(ctx context.Context, id string) error
	ListServersByDomainID(ctx context.Context, domainID string) ([]deployment.Server, error)
	ReplaceDomainServers(ctx context.Context, domainID string, serverIDs []string) error
}

type SQLServerRepository struct {
	db     *sql.DB
	driver string
}

func NewSQLServerRepository(db *sql.DB, driver ...string) *SQLServerRepository {
	return &SQLServerRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLServerRepository) query(value string) string {
	return Rebind(r.driver, value)
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
	validationCommand, err := json.Marshal(server.ValidationCommand)
	if err != nil {
		return fmt.Errorf("编码配置校验命令失败：%w", err)
	}
	createdAt := server.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := server.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err = r.db.ExecContext(ctx, r.query(`INSERT INTO servers
		(id, name, address, port, username, auth_method, private_key_ref, password_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, validation_command_json, reload_command_json, created_at, updated_at, project_id, owner_id, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), server.ID, server.Name, server.Address, server.Port, server.Username, server.AuthMethod, server.PasswordRef,
		server.HostKeyFingerprint, server.CertificatePath, server.PrivateKeyPath, server.CertificateChainPath, server.WebServer, string(validationCommand), string(command), createdAt, updatedAt,
		server.ProjectID, server.OwnerID, server.CreatedBy, server.UpdatedBy)
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
	validationCommand, err := json.Marshal(server.ValidationCommand)
	if err != nil {
		return fmt.Errorf("编码配置校验命令失败：%w", err)
	}
	updatedAt := server.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRowContext(ctx, r.query(`SELECT project_id FROM servers WHERE id = ?`+serverResourceLock(r.driver)), server.ID).Scan(&projectID); err != nil {
		return err
	}
	if projectID != server.ProjectID {
		return sql.ErrNoRows
	}
	if err := validateServerDomainPaths(ctx, tx, r.driver, server, nil); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, r.query(`UPDATE servers SET name = ?, address = ?, port = ?, username = ?, auth_method = ?, password_ref = ?, host_key_fingerprint = ?, certificate_path = ?, private_key_path = ?, certificate_chain_path = ?, web_server = ?, validation_command_json = ?, reload_command_json = ?, updated_at = ?, updated_by = ? WHERE id = ? AND project_id = ?`),
		server.Name, server.Address, server.Port, server.Username, server.AuthMethod, server.PasswordRef, server.HostKeyFingerprint, server.CertificatePath, server.PrivateKeyPath, server.CertificateChainPath, server.WebServer, string(validationCommand), string(command), updatedAt, server.UpdatedBy, server.ID, server.ProjectID)
	if err != nil {
		return fmt.Errorf("更新服务器失败：%w", err)
	}
	return tx.Commit()
}

func (r *SQLServerRepository) GetServerByID(ctx context.Context, id string) (deployment.Server, error) {
	row := r.db.QueryRowContext(ctx, r.query(serverSelect+` WHERE id = ?`), strings.TrimSpace(id))
	return scanServer(row)
}

func (r *SQLServerRepository) ListServers(ctx context.Context) ([]deployment.Server, error) {
	rows, err := r.db.QueryContext(ctx, r.query(serverSelect+` ORDER BY name, id`))
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
	if _, err := tx.ExecContext(ctx, r.query("DELETE FROM domain_servers WHERE server_id = ?"), id); err != nil {
		return rollback(fmt.Errorf("删除服务器关联失败：%w", err))
	}
	result, err := tx.ExecContext(ctx, r.query("DELETE FROM servers WHERE id = ?"), id)
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

func (r *SQLServerRepository) ListServersByDomainID(ctx context.Context, domainID string) ([]deployment.Server, error) {
	rows, err := r.db.QueryContext(ctx, r.query(serverSelectQualified+` INNER JOIN domain_servers ds ON ds.server_id = servers.id WHERE ds.domain_id = ? ORDER BY servers.name, servers.id`), strings.TrimSpace(domainID))
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
	domainID = strings.TrimSpace(domainID)
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
	sort.Strings(cleaned)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始更新域名服务器关联失败：%w", err)
	}
	defer tx.Rollback()
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	var candidate domain.Domain
	if err := tx.QueryRowContext(ctx, r.query(`SELECT id, project_id, name, certificate_type FROM domains WHERE id = ?`+serverResourceLock(r.driver)), domainID).Scan(&candidate.ID, &candidate.ProjectID, &candidate.Name, &candidate.CertificateType); err != nil {
		return rollback(fmt.Errorf("读取绑定域名项目失败：%w", err))
	}
	projectID := candidate.ProjectID
	if _, err := tx.ExecContext(ctx, r.query("DELETE FROM domain_servers WHERE domain_id = ?"), domainID); err != nil {
		return rollback(fmt.Errorf("清理域名服务器关联失败：%w", err))
	}
	for _, serverID := range cleaned {
		server, err := scanServer(tx.QueryRowContext(ctx, r.query(serverSelectQualified+` WHERE servers.id = ? AND servers.project_id = ?`+serverResourceLock(r.driver)), serverID, projectID))
		if err != nil {
			return rollback(fmt.Errorf("校验部署服务器失败：%w", err))
		}
		if err := validateServerDomainPaths(ctx, tx, r.driver, server, &candidate); err != nil {
			return rollback(err)
		}
		if _, err := tx.ExecContext(ctx, r.query("INSERT INTO domain_servers (domain_id, server_id, project_id) VALUES (?, ?, ?)"), domainID, serverID, projectID); err != nil {
			return rollback(fmt.Errorf("保存域名服务器关联失败：%w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交域名服务器关联失败：%w", err)
	}
	return nil
}

const serverSelect = `SELECT id, name, address, port, username, auth_method, password_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, validation_command_json, reload_command_json, created_at, updated_at, project_id, owner_id, created_by, updated_by FROM servers`
const serverSelectQualified = `SELECT servers.id, servers.name, servers.address, servers.port, servers.username, servers.auth_method, servers.password_ref, servers.host_key_fingerprint, servers.certificate_path, servers.private_key_path, servers.certificate_chain_path, servers.web_server, servers.validation_command_json, servers.reload_command_json, servers.created_at, servers.updated_at, servers.project_id, servers.owner_id, servers.created_by, servers.updated_by FROM servers`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (deployment.Server, error) {
	var server deployment.Server
	var webServer string
	var authMethod string
	var validationCommandJSON string
	var commandJSON string
	if err := row.Scan(&server.ID, &server.Name, &server.Address, &server.Port, &server.Username, &authMethod, &server.PasswordRef, &server.HostKeyFingerprint, &server.CertificatePath, &server.PrivateKeyPath, &server.CertificateChainPath, &webServer, &validationCommandJSON, &commandJSON, &server.CreatedAt, &server.UpdatedAt, &server.ProjectID, &server.OwnerID, &server.CreatedBy, &server.UpdatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deployment.Server{}, sql.ErrNoRows
		}
		return deployment.Server{}, fmt.Errorf("读取服务器失败：%w", err)
	}
	server.AuthMethod = deployment.SSHAuthMethod(authMethod)
	server.WebServer = deployment.WebServer(webServer)
	if err := json.Unmarshal([]byte(validationCommandJSON), &server.ValidationCommand); err != nil {
		return deployment.Server{}, fmt.Errorf("解析配置校验命令失败：%w", err)
	}
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
