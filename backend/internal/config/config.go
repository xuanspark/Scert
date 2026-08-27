package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const CurrentVersion = 1
const ConfigFormat = "json"
const LinuxInstallationMetadataPath = "/scert/conf/installation.json"

type installationMetadata struct {
	CertificateRoot string `json:"certificate_root"`
}

type Config struct {
	Version      int                `json:"version"`
	SetupReady   bool               `json:"setup_ready"`
	Admin        AdminConfig        `json:"admin"`
	Server       ServerConfig       `json:"server"`
	Web          WebConfig          `json:"web"`
	Database     DatabaseConfig     `json:"database"`
	Paths        PathsConfig        `json:"paths"`
	ACME         ACMEConfig         `json:"acme"`
	DNS          DNSConfig          `json:"dns"`
	Notification NotificationConfig `json:"notification"`
}

type AdminConfig struct {
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
}

type ServerConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	HTTPSMode string `json:"https_mode"`
	TLSCert   string `json:"tls_cert"`
	TLSKey    string `json:"tls_key"`
}

type WebConfig struct {
	Enabled   bool   `json:"enabled"`
	Hostname  string `json:"hostname"`
	PublicIP  string `json:"public_ip"`
	InitReady bool   `json:"init_ready"`
}

type DatabaseConfig struct {
	Driver              string `json:"driver"`
	DSN                 string `json:"dsn"`
	MaxOpenConns        int    `json:"max_open_conns"`
	MaxIdleConns        int    `json:"max_idle_conns"`
	ConnMaxLifetimeMins int    `json:"conn_max_lifetime_mins"`
}

type PathsConfig struct {
	Install     string `json:"install"`
	Certificate string `json:"certificate"`
	Config      string `json:"config"`
	Data        string `json:"data"`
	Log         string `json:"log"`
	Backup      string `json:"backup"`
}

type ACMEConfig struct {
	CA                   string `json:"ca"`
	Environment          string `json:"environment"`
	AccountEmail         string `json:"account_email"`
	RenewalThresholdDays int    `json:"renewal_threshold_days"`
}

type DNSConfig struct {
	Provider          string `json:"provider"`
	CredentialRef     string `json:"credential_ref"`
	VerificationState string `json:"verification_state"`
}

type NotificationConfig struct {
	HTTPEnabled bool     `json:"http_enabled"`
	SMTPEnabled bool     `json:"smtp_enabled"`
	Provider    string   `json:"provider"`
	Endpoint    string   `json:"endpoint"`
	Recipients  []string `json:"recipients"`
	TimeoutSecs int      `json:"timeout_secs"`
	RetryCount  int      `json:"retry_count"`
}

func Default(baseDir string) Config {
	paths := DefaultPaths(baseDir)
	return Config{
		Version:    CurrentVersion,
		SetupReady: false,
		Admin:      AdminConfig{},
		Server:     ServerConfig{Host: "0.0.0.0", Port: 8080, HTTPSMode: "direct"},
		Web:        WebConfig{Enabled: true},
		Database: DatabaseConfig{
			Driver: "sqlite", DSN: filepath.Join(paths.Data, "scert.db"),
			MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetimeMins: 30,
		},
		Paths:        paths,
		ACME:         ACMEConfig{CA: "letsencrypt", Environment: "staging", RenewalThresholdDays: 6},
		DNS:          DNSConfig{VerificationState: "not_configured"},
		Notification: NotificationConfig{TimeoutSecs: 10, RetryCount: 3},
	}
}

func DefaultPaths(baseDir string) PathsConfig {
	if runtime.GOOS == "linux" {
		certificateRoot := "/ssl"
		if strings.TrimSpace(baseDir) == "" {
			if installedRoot, err := readInstalledCertificateRoot(LinuxInstallationMetadataPath); err == nil {
				certificateRoot = installedRoot
			}
		}
		return PathsConfig{
			Install: "/scert", Certificate: certificateRoot, Config: "/scert/conf",
			Data: "/scert/data", Log: "/scert/log", Backup: "/scert/backups",
		}
	}
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	install := filepath.Join(baseDir, "Scert")
	return PathsConfig{
		Install: install, Certificate: filepath.Join(install, "ssl"),
		Config: filepath.Join(install, "config"), Data: filepath.Join(install, "data"),
		Log: filepath.Join(install, "logs"), Backup: filepath.Join(install, "backups"),
	}
}

func readInstalledCertificateRoot(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var metadata installationMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("解析安装路径记录失败：%w", err)
	}
	root := filepath.Clean(strings.TrimSpace(metadata.CertificateRoot))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return "", fmt.Errorf("安装路径记录中的证书目录无效")
	}
	return root, nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置文件失败：%w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件失败：%w", err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Normalize() {
	if c == nil {
		return
	}
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.HTTPSMode == "" {
		c.Server.HTTPSMode = "direct"
	}
	if c.Web.Hostname == "" && c.Web.PublicIP == "" && !c.SetupReady {
		c.Web.Enabled = true
	}
	if c.DNS.VerificationState == "" {
		c.DNS.VerificationState = "not_configured"
	}
}
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败：%w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("编码配置失败：%w", err)
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("写入临时配置文件失败：%w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("替换配置文件失败：%w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置配置文件权限失败：%w", err)
	}
	return nil
}

func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("配置版本不兼容：%d", c.Version)
	}
	if strings.TrimSpace(c.Server.Host) == "" {
		return fmt.Errorf("服务监听地址不能为空")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("服务监听端口无效：%d", c.Server.Port)
	}
	if c.Server.HTTPSMode != "direct" && c.Server.HTTPSMode != "proxy" {
		return fmt.Errorf("HTTPS 模式无效：%s", c.Server.HTTPSMode)
	}
	if c.Database.Driver != "sqlite" && c.Database.Driver != "mysql" {
		return fmt.Errorf("数据库驱动无效：%s", c.Database.Driver)
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("数据库连接信息不能为空")
	}
	if c.Database.MaxOpenConns < 1 || c.Database.MaxIdleConns < 0 || c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return fmt.Errorf("数据库连接池配置无效")
	}
	if c.Database.ConnMaxLifetimeMins < 0 {
		return fmt.Errorf("数据库连接生命周期不能为负数")
	}
	for name, value := range map[string]string{
		"安装目录": c.Paths.Install, "证书目录": c.Paths.Certificate, "配置目录": c.Paths.Config,
		"数据目录": c.Paths.Data, "日志目录": c.Paths.Log, "备份目录": c.Paths.Backup,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s不能为空", name)
		}
	}
	if c.ACME.CA != "letsencrypt" {
		return fmt.Errorf("当前仅支持 Let's Encrypt")
	}
	if c.ACME.Environment != "staging" && c.ACME.Environment != "production" {
		return fmt.Errorf("ACME 环境无效：%s", c.ACME.Environment)
	}
	if c.ACME.RenewalThresholdDays < 1 {
		return fmt.Errorf("续期阈值必须大于 0")
	}
	if c.DNS.VerificationState != "not_configured" && c.DNS.VerificationState != "pending" && c.DNS.VerificationState != "verified" && c.DNS.VerificationState != "failed" {
		return fmt.Errorf("DNS 验证状态无效：%s", c.DNS.VerificationState)
	}
	if c.SetupReady {
		if strings.TrimSpace(c.Admin.Username) == "" || strings.TrimSpace(c.Admin.PasswordHash) == "" {
			return fmt.Errorf("初始化完成时必须配置超级管理员账号")
		}
		adminEmail := strings.ToLower(strings.TrimSpace(c.Admin.Email))
		parsedEmail, emailErr := mail.ParseAddress(adminEmail)
		if adminEmail == "" || emailErr != nil || parsedEmail.Address != adminEmail || parsedEmail.Name != "" {
			return fmt.Errorf("初始化完成时必须配置合法的超级管理员绑定邮箱")
		}
		if c.DNS.VerificationState != "verified" {
			return fmt.Errorf("初始化完成时必须先通过 DNS API 验证")
		}
		if strings.TrimSpace(c.Web.Hostname) == "" && strings.TrimSpace(c.Web.PublicIP) == "" {
			return fmt.Errorf("初始化完成时必须配置管理页面域名或公网 IP")
		}
	}
	if c.Notification.TimeoutSecs < 1 || c.Notification.RetryCount < 0 {
		return fmt.Errorf("通知配置无效")
	}
	return nil
}

// ResetURL 返回通过主 Web 服务访问密码重置入口的公开地址。
func (c Config) ResetURL() string {
	host := strings.TrimSpace(c.Web.Hostname)
	if host == "" {
		host = strings.TrimSpace(c.Web.PublicIP)
	}
	if host == "" {
		return ""
	}
	if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: "https", Host: host, Path: "/reset"}).String()
}
