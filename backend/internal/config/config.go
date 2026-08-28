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
	HTTPEnabled              bool              `json:"http_enabled"`
	SMTPEnabled              bool              `json:"smtp_enabled"`
	Provider                 string            `json:"provider"`
	Endpoint                 string            `json:"endpoint"`
	HTTPPath                 string            `json:"http_path"`
	HTTPLoginPath            string            `json:"http_login_path"`
	HTTPAuthMode             string            `json:"http_auth_mode"`
	HTTPCredentialRef        string            `json:"http_credential_ref"`
	HTTPHeaders              map[string]string `json:"http_headers"`
	EventMode                string            `json:"event_mode"`
	IncludeDeploymentDetails bool              `json:"include_deployment_details"`
	IncludeCertificateSerial bool              `json:"include_certificate_serial"`
	IncludeManagementURL     bool              `json:"include_management_url"`
	SenderName               string            `json:"sender_name"`
	SenderEmail              string            `json:"sender_email"`
	Recipients               []string          `json:"recipients"`
	TimeoutSecs              int               `json:"timeout_secs"`
	RetryCount               int               `json:"retry_count"`
	RetryIntervalsSecs       []int             `json:"retry_intervals_secs"`
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
		Paths: paths,
		ACME:  ACMEConfig{CA: "letsencrypt", Environment: "staging", RenewalThresholdDays: 6},
		DNS:   DNSConfig{VerificationState: "not_configured"},
		Notification: NotificationConfig{
			HTTPHeaders:        map[string]string{},
			HTTPPath:           "/api/email/send",
			HTTPLoginPath:      "/api/login",
			HTTPAuthMode:       "none",
			EventMode:          "detailed",
			SenderName:         "Scert",
			TimeoutSecs:        10,
			RetryCount:         3,
			RetryIntervalsSecs: []int{30, 120, 600},
		},
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
	if c.Notification.HTTPPath == "" {
		c.Notification.HTTPPath = "/api/email/send"
	}
	if c.Notification.HTTPLoginPath == "" {
		c.Notification.HTTPLoginPath = "/api/login"
	}
	if c.Notification.HTTPAuthMode == "" {
		c.Notification.HTTPAuthMode = "none"
	}
	if c.Notification.EventMode == "" {
		c.Notification.EventMode = "detailed"
	}
	if c.Notification.SenderName == "" {
		c.Notification.SenderName = "Scert"
	}
	if c.Notification.TimeoutSecs == 0 {
		c.Notification.TimeoutSecs = 10
	}
	if c.Notification.RetryCount == 0 {
		c.Notification.RetryCount = 3
	}
	if len(c.Notification.RetryIntervalsSecs) == 0 {
		c.Notification.RetryIntervalsSecs = []int{30, 120, 600}
	}
	if c.Notification.HTTPHeaders == nil {
		c.Notification.HTTPHeaders = map[string]string{}
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
	if c.Notification.HTTPAuthMode != "none" && c.Notification.HTTPAuthMode != "bearer" && c.Notification.HTTPAuthMode != "hmac_sha256" && c.Notification.HTTPAuthMode != "session" {
		return fmt.Errorf("HTTP 通知认证方式无效：%s", c.Notification.HTTPAuthMode)
	}
	if c.Notification.HTTPAuthMode != "none" && strings.TrimSpace(c.Notification.HTTPCredentialRef) == "" {
		return fmt.Errorf("HTTP 通知启用认证时必须配置凭据引用")
	}
	if c.Notification.EventMode != "detailed" && c.Notification.EventMode != "unified" {
		return fmt.Errorf("通知事件模式无效：%s", c.Notification.EventMode)
	}
	if strings.TrimSpace(c.Notification.HTTPPath) == "" || !strings.HasPrefix(strings.TrimSpace(c.Notification.HTTPPath), "/") {
		return fmt.Errorf("HTTP 通知请求路径必须以 / 开头")
	}
	if strings.TrimSpace(c.Notification.HTTPLoginPath) == "" || !strings.HasPrefix(strings.TrimSpace(c.Notification.HTTPLoginPath), "/") {
		return fmt.Errorf("HTTP 通知登录请求路径必须以 / 开头")
	}
	if endpoint := strings.TrimSpace(c.Notification.Endpoint); endpoint != "" {
		parsedEndpoint, endpointErr := url.Parse(endpoint)
		if endpointErr != nil || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || parsedEndpoint.Host == "" {
			return fmt.Errorf("第三方邮箱 API 地址格式无效")
		}
	}
	if c.Notification.SenderEmail != "" {
		senderEmail := strings.ToLower(strings.TrimSpace(c.Notification.SenderEmail))
		parsedSender, senderErr := mail.ParseAddress(senderEmail)
		if senderErr != nil || parsedSender.Address != senderEmail || parsedSender.Name != "" {
			return fmt.Errorf("通知发件人地址格式无效")
		}
	}
	if c.Notification.TimeoutSecs < 1 || c.Notification.RetryCount < 0 || len(c.Notification.RetryIntervalsSecs) != c.Notification.RetryCount {
		return fmt.Errorf("通知配置无效")
	}
	for _, interval := range c.Notification.RetryIntervalsSecs {
		if interval < 1 {
			return fmt.Errorf("通知重试间隔必须大于 0")
		}
	}
	for header := range c.Notification.HTTPHeaders {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "authorization", "cookie", "origin", "referer", "content-type", "accept", "x-csrf-token", "x-scert-signature", "x-scert-request-id", "x-scert-timestamp":
			return fmt.Errorf("HTTP 自定义请求头不允许覆盖安全头：%s", header)
		}
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
