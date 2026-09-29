package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
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
	Host           string `json:"host"`
	Port           int    `json:"port"`
	HTTPSMode      string `json:"https_mode"`
	ProxyHTTPSPort int    `json:"proxy_https_port,omitempty"`
	TLSCert        string `json:"tls_cert"`
	TLSKey         string `json:"tls_key"`
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
	CredentialRef       string `json:"credential_ref"`
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
	CA                             string `json:"ca"`
	Environment                    string `json:"environment"`
	AccountEmail                   string `json:"account_email"`
	RenewalCheckIntervalDays       int    `json:"renewal_check_interval_days"`
	RenewalNearExpiryDays          int    `json:"renewal_near_expiry_days"`
	RenewalThresholdDays           int    `json:"renewal_threshold_days"`
	RenewalUrgentThresholdDays     int    `json:"renewal_urgent_threshold_days"`
	RenewalMaxRetries              int    `json:"renewal_max_retries"`
	RenewalRetryIntervalMins       int    `json:"renewal_retry_interval_mins"`
	PropagationTimeoutSecs         int    `json:"propagation_timeout_secs"`
	PropagationPollingIntervalSecs int    `json:"propagation_polling_interval_secs"`
}

type DNSConfig struct {
	ProjectID         string `json:"project_id,omitempty"`
	OwnerUserID       string `json:"owner_user_id,omitempty"`
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
	SMTPHost                 string            `json:"smtp_host"`
	SMTPPort                 int               `json:"smtp_port"`
	SMTPUsername             string            `json:"smtp_username"`
	SMTPCredentialRef        string            `json:"smtp_credential_ref"`
	SMTPMode                 string            `json:"smtp_mode"`
	SMTPAllowPlaintextAuth   bool              `json:"smtp_allow_plaintext_auth"`
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
		ACME: ACMEConfig{
			CA:                             "letsencrypt",
			Environment:                    "staging",
			RenewalCheckIntervalDays:       3,
			RenewalNearExpiryDays:          10,
			RenewalThresholdDays:           6,
			RenewalUrgentThresholdDays:     3,
			RenewalMaxRetries:              3,
			RenewalRetryIntervalMins:       30,
			PropagationTimeoutSecs:         300,
			PropagationPollingIntervalSecs: 5,
		},
		DNS: DNSConfig{ProjectID: "default", VerificationState: "not_configured"},
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
			SMTPPort:           587,
			SMTPMode:           "starttls",
		},
	}
}

func DefaultPaths(baseDir string) PathsConfig {
	if runtime.GOOS == "linux" {
		certificateRoot := "/ssl/scert"
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

// LoadWithEnv 读取配置文件，并应用受控的 SCERT_* 环境变量覆盖。
// 环境变量只影响当前进程，不会回写配置文件。
func LoadWithEnv(path string) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.ApplyEnvironment(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ApplyEnvironment 将部署环境常用配置映射到当前配置。
// 未设置的变量保持配置文件中的值，敏感值只从环境读取，不写入日志或配置文件。
func (c *Config) ApplyEnvironment() error {
	if c == nil {
		return fmt.Errorf("配置不能为空")
	}
	stringOverrides := []struct {
		name   string
		target *string
	}{
		{"SCERT_SERVER_HOST", &c.Server.Host},
		{"SCERT_SERVER_HTTPS_MODE", &c.Server.HTTPSMode},
		{"SCERT_WEB_HOSTNAME", &c.Web.Hostname},
		{"SCERT_WEB_PUBLIC_IP", &c.Web.PublicIP},
		{"SCERT_DATABASE_DRIVER", &c.Database.Driver},
		{"SCERT_DATABASE_DSN", &c.Database.DSN},
		{"SCERT_DATABASE_CREDENTIAL_REF", &c.Database.CredentialRef},
		{"SCERT_ACME_ENVIRONMENT", &c.ACME.Environment},
		{"SCERT_ACME_ACCOUNT_EMAIL", &c.ACME.AccountEmail},
		{"SCERT_DNS_PROVIDER", &c.DNS.Provider},
		{"SCERT_DNS_CREDENTIAL_REF", &c.DNS.CredentialRef},
		{"SCERT_NOTIFICATION_HTTP_CREDENTIAL_REF", &c.Notification.HTTPCredentialRef},
		{"SCERT_NOTIFICATION_SMTP_CREDENTIAL_REF", &c.Notification.SMTPCredentialRef},
	}
	for _, override := range stringOverrides {
		if value, ok := os.LookupEnv(override.name); ok {
			*override.target = strings.TrimSpace(value)
		}
	}
	boolOverrides := []struct {
		name   string
		target *bool
	}{
		{"SCERT_WEB_ENABLED", &c.Web.Enabled},
	}
	for _, override := range boolOverrides {
		value, ok := os.LookupEnv(override.name)
		if !ok {
			continue
		}
		parsed, err := parseEnvironmentBool(override.name, value)
		if err != nil {
			return err
		}
		*override.target = parsed
	}
	intOverrides := []struct {
		name   string
		target *int
	}{
		{"SCERT_SERVER_PORT", &c.Server.Port},
		{"SCERT_SERVER_PROXY_HTTPS_PORT", &c.Server.ProxyHTTPSPort},
		{"SCERT_ACME_RENEWAL_CHECK_INTERVAL_DAYS", &c.ACME.RenewalCheckIntervalDays},
		{"SCERT_ACME_RENEWAL_NEAR_EXPIRY_DAYS", &c.ACME.RenewalNearExpiryDays},
		{"SCERT_ACME_RENEWAL_THRESHOLD_DAYS", &c.ACME.RenewalThresholdDays},
		{"SCERT_ACME_RENEWAL_URGENT_THRESHOLD_DAYS", &c.ACME.RenewalUrgentThresholdDays},
		{"SCERT_ACME_RENEWAL_MAX_RETRIES", &c.ACME.RenewalMaxRetries},
		{"SCERT_ACME_RENEWAL_RETRY_INTERVAL_MINS", &c.ACME.RenewalRetryIntervalMins},
		{"SCERT_ACME_PROPAGATION_TIMEOUT_SECS", &c.ACME.PropagationTimeoutSecs},
		{"SCERT_ACME_PROPAGATION_POLLING_INTERVAL_SECS", &c.ACME.PropagationPollingIntervalSecs},
	}
	for _, override := range intOverrides {
		value, ok := os.LookupEnv(override.name)
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("环境变量 %s 必须是整数：%w", override.name, err)
		}
		*override.target = parsed
	}
	return nil
}

func parseEnvironmentBool(name, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("环境变量 %s 必须是 true 或 false", name)
	}
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
	c.Database.Driver = strings.ToLower(strings.TrimSpace(c.Database.Driver))
	c.Database.DSN = strings.TrimSpace(c.Database.DSN)
	c.Database.CredentialRef = strings.TrimSpace(c.Database.CredentialRef)
	if c.Web.Hostname == "" && c.Web.PublicIP == "" && !c.SetupReady {
		c.Web.Enabled = true
	}
	if c.DNS.VerificationState == "" {
		c.DNS.VerificationState = "not_configured"
	}
	if strings.TrimSpace(c.DNS.ProjectID) == "" {
		c.DNS.ProjectID = "default"
	}
	if c.ACME.PropagationTimeoutSecs == 0 {
		c.ACME.PropagationTimeoutSecs = 300
	}
	if c.ACME.RenewalCheckIntervalDays == 0 {
		c.ACME.RenewalCheckIntervalDays = 3
	}
	if c.ACME.RenewalNearExpiryDays == 0 {
		c.ACME.RenewalNearExpiryDays = 10
	}
	if c.ACME.RenewalThresholdDays == 0 {
		c.ACME.RenewalThresholdDays = 6
	}
	if c.ACME.RenewalUrgentThresholdDays == 0 {
		c.ACME.RenewalUrgentThresholdDays = 3
	}
	if c.ACME.RenewalMaxRetries == 0 {
		c.ACME.RenewalMaxRetries = 3
	}
	if c.ACME.RenewalRetryIntervalMins == 0 {
		c.ACME.RenewalRetryIntervalMins = 30
	}
	if c.ACME.PropagationPollingIntervalSecs == 0 {
		c.ACME.PropagationPollingIntervalSecs = 5
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
	// 空数组表示明确关闭重试，只有旧配置未提供间隔字段时才补默认值。
	if c.Notification.RetryCount == 0 && c.Notification.RetryIntervalsSecs == nil {
		c.Notification.RetryCount = 3
	}
	if c.Notification.RetryIntervalsSecs == nil {
		c.Notification.RetryIntervalsSecs = []int{30, 120, 600}
	}
	if c.Notification.HTTPHeaders == nil {
		c.Notification.HTTPHeaders = map[string]string{}
	}
	if c.Notification.SMTPPort == 0 {
		c.Notification.SMTPPort = 587
	}
	if c.Notification.SMTPMode == "" {
		c.Notification.SMTPMode = "starttls"
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
	return saveConfigFile(path, data, defaultConfigSaveOperations())
}

// applyLinuxServiceOwnership 在 root 初始化配置时，将配置目录和文件交给 Scert 服务用户管理。
// 非 Linux、非 root 进程或系统尚未创建 Scert 用户时保持原有行为，便于开发环境使用。
func applyLinuxServiceOwnership(directory, path string) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return nil
	}

	serviceUser, err := user.Lookup("Scert")
	if err != nil {
		return nil
	}
	serviceGroup, err := user.LookupGroup("Scert")
	if err != nil {
		return nil
	}
	uid, err := strconv.Atoi(serviceUser.Uid)
	if err != nil {
		return fmt.Errorf("解析 Scert 用户编号失败：%w", err)
	}
	gid, err := strconv.Atoi(serviceGroup.Gid)
	if err != nil {
		return fmt.Errorf("解析 Scert 用户组编号失败：%w", err)
	}
	if err := os.Chown(directory, uid, gid); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	return os.Chown(path, uid, gid)
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
	if c.Server.ProxyHTTPSPort < 0 || c.Server.ProxyHTTPSPort > 65535 {
		return fmt.Errorf("代理 HTTPS 端口必须为 1 至 65535，或使用 0 表示默认 443")
	}
	if c.Database.Driver != "sqlite" && c.Database.Driver != "mysql" && c.Database.Driver != "postgres" {
		return fmt.Errorf("数据库驱动无效：%s", c.Database.Driver)
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("数据库连接信息不能为空")
	}
	if c.Database.Driver == "sqlite" {
		if strings.TrimSpace(c.Database.CredentialRef) != "" {
			return fmt.Errorf("SQLite 不允许配置数据库凭据引用")
		}
	} else {
		if strings.TrimSpace(c.Database.CredentialRef) == "" {
			return fmt.Errorf("外部数据库必须配置加密凭据引用")
		}
		if !strings.HasPrefix(strings.TrimSpace(c.Database.CredentialRef), "secret://database/") {
			return fmt.Errorf("数据库凭据引用必须使用 secret://database/ 前缀")
		}
		if databaseDSNContainsPassword(c.Database.Driver, c.Database.DSN) {
			return fmt.Errorf("数据库连接信息不能包含密码，请使用加密凭据引用")
		}
		if c.Database.Driver == "postgres" {
			if err := ValidatePostgresDSN(c.Database.DSN); err != nil {
				return err
			}
		}
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
	if c.ACME.RenewalCheckIntervalDays < 1 || c.ACME.RenewalNearExpiryDays < 1 || c.ACME.RenewalUrgentThresholdDays < 1 {
		return fmt.Errorf("续期检查阈值必须大于 0")
	}
	if c.ACME.RenewalUrgentThresholdDays > c.ACME.RenewalThresholdDays {
		return fmt.Errorf("紧急续期阈值不能大于自动续期阈值")
	}
	if c.ACME.RenewalThresholdDays > c.ACME.RenewalNearExpiryDays {
		return fmt.Errorf("自动续期阈值不能大于临近到期检查阈值")
	}
	if c.ACME.RenewalMaxRetries < 1 || c.ACME.RenewalMaxRetries > 10 {
		return fmt.Errorf("续期最大重试次数必须在 1 到 10 次之间")
	}
	if c.ACME.RenewalRetryIntervalMins < 1 || c.ACME.RenewalRetryIntervalMins > 1440 {
		return fmt.Errorf("续期重试间隔必须在 1 到 1440 分钟之间")
	}
	if c.ACME.PropagationTimeoutSecs < 1 || c.ACME.PropagationTimeoutSecs > 86400 {
		return fmt.Errorf("DNS 传播等待时间必须在 1 到 86400 秒之间")
	}
	if c.ACME.PropagationPollingIntervalSecs < 1 {
		return fmt.Errorf("DNS 传播检测间隔必须大于 0")
	}
	if c.ACME.PropagationPollingIntervalSecs > c.ACME.PropagationTimeoutSecs {
		return fmt.Errorf("DNS 传播检测间隔不能大于传播等待时间")
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
	if c.Notification.SMTPMode != "plain" && c.Notification.SMTPMode != "starttls" && c.Notification.SMTPMode != "implicit_tls" {
		return fmt.Errorf("\u0053\u004d\u0054\u0050 \u8fde\u63a5\u6a21\u5f0f\u65e0\u6548\uff1a%s", c.Notification.SMTPMode)
	}
	if c.Notification.SMTPEnabled {
		if strings.TrimSpace(c.Notification.SMTPHost) == "" {
			return fmt.Errorf("\u542f\u7528 SMTP \u901a\u77e5\u65f6\u5fc5\u987b\u914d\u7f6e SMTP \u4e3b\u673a")
		}
		if c.Notification.SMTPPort < 1 || c.Notification.SMTPPort > 65535 {
			return fmt.Errorf("SMTP \u7aef\u53e3\u65e0\u6548\uff1a%d", c.Notification.SMTPPort)
		}
		if strings.TrimSpace(c.Notification.SMTPUsername) != "" && strings.TrimSpace(c.Notification.SMTPCredentialRef) == "" {
			return fmt.Errorf("SMTP \u914d\u7f6e\u7528\u6237\u540d\u540e\u5fc5\u987b\u914d\u7f6e\u52a0\u5bc6\u51ed\u636e\u5f15\u7528")
		}
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

func databaseDSNContainsPassword(driver, dsn string) bool {
	driver = strings.ToLower(strings.TrimSpace(driver))
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return false
	}
	switch driver {
	case "mysql":
		at := strings.Index(dsn, "@")
		if at < 0 {
			return false
		}
		credentials := dsn[:at]
		return strings.Contains(credentials, ":")
	case "postgres":
		if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
			if parsed.User == nil {
				return false
			}
			_, present := parsed.User.Password()
			return present
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

// ManagementURL 使用公开入口端口，代理模式不暴露内部监听端口。
func (c Config) ManagementURL() string {
	rawHost := strings.TrimSpace(c.Web.Hostname)
	if rawHost == "" {
		rawHost = strings.TrimSpace(c.Web.PublicIP)
	}
	if rawHost == "" {
		return ""
	}
	host, _ := splitResetHost(rawHost)
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	port := c.Server.Port
	if c.Server.HTTPSMode == "proxy" {
		port = c.Server.ProxyHTTPSPort
		if port == 0 {
			port = 443
		}
	}
	if port != 443 {
		host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	}
	return (&url.URL{Scheme: "https", Host: host, Path: "/"}).String()
}

// ResetURL 与管理入口共用域名和端口，不增加独立监听服务。
func (c Config) ResetURL() string {
	address := c.ManagementURL()
	if address == "" {
		return ""
	}
	return address + "reset"
}

func splitResetHost(rawHost string) (string, string) {
	trimmed := strings.TrimSpace(rawHost)
	withoutBrackets := strings.Trim(trimmed, "[]")
	if net.ParseIP(withoutBrackets) != nil {
		return withoutBrackets, ""
	}

	parseTarget := trimmed
	if !strings.Contains(parseTarget, "://") {
		parseTarget = "//" + parseTarget
	}
	if parsed, err := url.Parse(parseTarget); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname(), parsed.Port()
	}
	if parsedHost, parsedPort, err := net.SplitHostPort(trimmed); err == nil {
		return parsedHost, parsedPort
	}
	return withoutBrackets, ""
}
