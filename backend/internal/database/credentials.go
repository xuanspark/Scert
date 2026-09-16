package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// CredentialReader 读取已加密保存的数据库用户名和密码。
// 数据库密码只能在调用期间留存在内存中，不能保存到普通配置文件。
type CredentialReader interface {
	Get(context.Context, string) (map[string]string, error)
}

// NewCredentialReference 为外部数据库生成不可预测的加密凭据引用。
func NewCredentialReference() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成数据库凭据引用失败：%w", err)
	}
	return "secret://database/" + hex.EncodeToString(buffer), nil
}

// ValidateConfig 校验可持久化的数据库配置。外部数据库的 DSN 不能包含密码。
func ValidateConfig(cfg config.DatabaseConfig) error {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	dsn := strings.TrimSpace(cfg.DSN)
	credentialRef := strings.TrimSpace(cfg.CredentialRef)
	if driver != "sqlite" && driver != "mysql" && driver != "postgres" {
		return fmt.Errorf("数据库驱动无效：%s", cfg.Driver)
	}
	if dsn == "" {
		return fmt.Errorf("数据库连接信息不能为空")
	}
	if driver == "sqlite" {
		if credentialRef != "" {
			return fmt.Errorf("SQLite 不允许配置数据库凭据引用")
		}
		return nil
	}
	if credentialRef == "" || !strings.HasPrefix(credentialRef, "secret://database/") {
		return fmt.Errorf("外部数据库必须配置 secret://database/ 加密凭据引用")
	}
	if containsPassword(driver, dsn) {
		return fmt.Errorf("数据库连接信息不能包含密码，请使用加密凭据引用")
	}
	if driver == "mysql" {
		parsed, err := mysql.ParseDSN(dsn)
		if err != nil {
			return fmt.Errorf("解析 MySQL 连接信息失败：%w", err)
		}
		if strings.TrimSpace(parsed.User) == "" {
			return fmt.Errorf("MySQL 连接信息必须包含用户名")
		}
		return nil
	}
	if _, err := normalizePostgresDSN(dsn); err != nil {
		return err
	}
	return nil
}

// ResolveConfig 从凭据存储中读取外部数据库账号，在内存中构造连接配置。
// 返回值绝不能保存或记录到日志。
func ResolveConfig(ctx context.Context, cfg config.DatabaseConfig, reader CredentialReader) (config.DatabaseConfig, error) {
	if err := ValidateConfig(cfg); err != nil {
		return config.DatabaseConfig{}, err
	}
	resolved := cfg
	if strings.EqualFold(resolved.Driver, "sqlite") {
		return resolved, nil
	}
	if reader == nil {
		return config.DatabaseConfig{}, fmt.Errorf("数据库敏感凭据存储未初始化")
	}
	credentials, err := reader.Get(ctx, resolved.CredentialRef)
	if err != nil {
		return config.DatabaseConfig{}, fmt.Errorf("读取数据库加密凭据失败：%w", err)
	}
	username := strings.TrimSpace(credentials["username"])
	password, exists := credentials["password"]
	if username == "" || !exists || password == "" {
		return config.DatabaseConfig{}, fmt.Errorf("数据库加密凭据必须包含用户名和密码")
	}
	if strings.EqualFold(resolved.Driver, "mysql") {
		dsn, err := withMySQLCredentials(resolved.DSN, username, password)
		if err != nil {
			return config.DatabaseConfig{}, err
		}
		resolved.DSN = dsn
		return resolved, nil
	}
	dsn, err := withPostgresCredentials(resolved.DSN, username, password)
	if err != nil {
		return config.DatabaseConfig{}, err
	}
	resolved.DSN = dsn
	return resolved, nil
}

func withMySQLCredentials(dsn, username, password string) (string, error) {
	parsed, err := mysql.ParseDSN(strings.TrimSpace(dsn))
	if err != nil {
		return "", fmt.Errorf("解析 MySQL 连接信息失败：%w", err)
	}
	parsed.User = username
	parsed.Passwd = password
	parsed.ParseTime = true
	parsed.Loc = time.UTC
	return parsed.FormatDSN(), nil
}

func withPostgresCredentials(dsn, username, password string) (string, error) {
	raw := strings.TrimSpace(dsn)
	parsedURL, err := url.Parse(raw)
	if err != nil || (parsedURL.Scheme != "postgres" && parsedURL.Scheme != "postgresql") {
		return "", fmt.Errorf("PostgreSQL 数据库连接信息必须使用不含密码的 postgres:// URL")
	}
	parsedURL.User = url.UserPassword(username, password)
	return parsedURL.String(), nil
}

func containsPassword(driver, dsn string) bool {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql":
		at := strings.Index(dsn, "@")
		return at >= 0 && strings.Contains(dsn[:at], ":")
	case "postgres":
		if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
			if parsed.User == nil {
				return false
			}
			_, exists := parsed.User.Password()
			return exists
		}
		for _, field := range strings.Fields(dsn) {
			key, _, found := strings.Cut(field, "=")
			if found && strings.EqualFold(strings.TrimSpace(key), "password") {
				return true
			}
		}
	}
	return false
}
