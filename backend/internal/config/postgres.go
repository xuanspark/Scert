package config

import (
	"errors"
	"net/url"
	"strings"
)

// ValidatePostgresDSN 同时用于配置落盘和数据库连接前检查，不读取环境或外部文件。
// 账号只能由加密凭据注入；查询参数不能改写验证过的主机、数据库或凭据来源。
func ValidatePostgresDSN(dsn string) error {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return errors.New("PostgreSQL 数据库连接信息必须使用不含账号密码的 postgres:// URL")
	}
	if parsed.User != nil {
		return errors.New("PostgreSQL 用户名和密码只能保存到加密凭据中")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" || strings.Trim(strings.TrimSpace(parsed.Path), "/") == "" {
		return errors.New("PostgreSQL 连接信息必须包含主机和数据库")
	}
	if parsed.Opaque != "" || parsed.Fragment != "" {
		return errors.New("PostgreSQL 连接信息不能包含片段或不透明地址")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return errors.New("PostgreSQL 连接参数编码无效")
	}
	for key, values := range query {
		if key == "" || key != strings.ToLower(strings.TrimSpace(key)) || len(values) != 1 {
			return errors.New("PostgreSQL 连接参数必须为小写且不能重复")
		}
		switch key {
		case "user", "password", "sslpassword", "host", "hostaddr", "port", "dbname", "database",
			"service", "servicefile", "passfile":
			return errors.New("PostgreSQL 查询参数不能包含账号密码或覆盖连接目标及凭据来源")
		}
	}
	if query.Get("sslmode") != "verify-full" {
		return errors.New("PostgreSQL 连接必须使用 sslmode=verify-full")
	}
	return nil
}
