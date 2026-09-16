package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

var (
	ErrCompleteBusy = errors.New("初始化正在执行")
	ErrCompleteDone = errors.New("初始化已经完成")
)

type CompleteErrorKind string

const (
	CompleteErrorInput        CompleteErrorKind = "input"
	CompleteErrorVerification CompleteErrorKind = "verification"
	CompleteErrorDependency   CompleteErrorKind = "dependency"
	CompleteErrorInternal     CompleteErrorKind = "internal"
)

// CompleteError 只向 HTTP 层公开安全分类和通用说明，底层错误不得直接返回浏览器。
type CompleteError struct {
	Kind    CompleteErrorKind
	Message string
	Err     error
}

func (err *CompleteError) Error() string {
	if err == nil {
		return "初始化失败"
	}
	return err.Message
}

func (err *CompleteError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

type CompleteRequest struct {
	DatabaseVerificationToken      string `json:"database_verification_token"`
	DNSVerificationToken           string `json:"dns_verification_token"`
	AdminUsername                  string `json:"admin_username"`
	AdminEmail                     string `json:"admin_email"`
	AdminPassword                  string `json:"admin_password"`
	ServerPort                     int    `json:"server_port"`
	WebHostname                    string `json:"web_hostname"`
	WebPublicIP                    string `json:"web_public_ip"`
	WebEnabled                     bool   `json:"web_enabled"`
	HTTPSMode                      string `json:"https_mode"`
	ACMEEnvironment                string `json:"acme_environment"`
	PropagationTimeoutSecs         int    `json:"propagation_timeout_secs"`
	PropagationPollingIntervalSecs int    `json:"propagation_polling_interval_secs"`
}

type CompleteResult struct {
	Completed     bool     `json:"completed"`
	ManagementURL string   `json:"management_url"`
	Warnings      []string `json:"warnings,omitempty"`
}

type completionDatabaseSessions interface {
	Claim(string) (DatabaseVerificationSession, func() error, error)
	DiscardAllExcept(...string)
}

type completionDNSSessions interface {
	Claim(string) (setup.DNSVerificationSession, func() error, error)
	DiscardAllExcept(...string)
}

type setupCertificateIssuer interface {
	IssueForSetup(context.Context, certificate.SetupCertificateRequest) (certificate.SavedResource, error)
}

type completionDatabaseOpener func(context.Context, config.DatabaseConfig) (*database.Database, error)
type completionMigrator func(context.Context, *sql.DB, string) error
type completionAdminEnsurer func(context.Context, *sql.DB, string, repository.User) error
type completionIPIssuer func(context.Context, string, string) (ManagementCertificate, error)
type completionConfigSaver func(config.Config, string) error
type completionCertificateValidator func(context.Context, string, string, string, []string) error

type CompleteDependencies struct {
	BaseConfig       config.Config
	ConfigPath       string
	BootstrapURLPath string
	DatabaseSessions completionDatabaseSessions
	DNSSessions      completionDNSSessions
	Credentials      database.CredentialReader
	Sessions         *SessionManager
	Firewall         firewall.Manager
	Certificate      setupCertificateIssuer
	OpenDatabase     completionDatabaseOpener
	Migrate          completionMigrator
	EnsureAdmin      completionAdminEnsurer
	IssueIP          completionIPIssuer
	SaveConfig       completionConfigSaver
	ValidateFiles    completionCertificateValidator
	RollbackDomain   func(context.Context, certificate.SavedResource) error
	RollbackIP       func(context.Context, ManagementCertificate) error
	RemoveFile       func(string) error
	Warn             func(string)
}

// Completer 串行执行首次初始化，并以配置原子保存作为唯一提交点。
type Completer struct {
	mu               sync.Mutex
	base             config.Config
	configPath       string
	bootstrapURLPath string
	databaseSessions completionDatabaseSessions
	dnsSessions      completionDNSSessions
	credentials      database.CredentialReader
	sessions         *SessionManager
	firewall         firewall.Manager
	certificate      setupCertificateIssuer
	openDatabase     completionDatabaseOpener
	migrate          completionMigrator
	ensureAdmin      completionAdminEnsurer
	issueIP          completionIPIssuer
	saveConfig       completionConfigSaver
	validateFiles    completionCertificateValidator
	rollbackDomain   func(context.Context, certificate.SavedResource) error
	rollbackIP       func(context.Context, ManagementCertificate) error
	removeFile       func(string) error
	warn             func(string)
	completed        bool
}

func NewCompleter(dependencies CompleteDependencies) *Completer {
	if dependencies.OpenDatabase == nil {
		dependencies.OpenDatabase = database.Open
	}
	if dependencies.Migrate == nil {
		dependencies.Migrate = database.Migrate
	}
	if dependencies.EnsureAdmin == nil {
		dependencies.EnsureAdmin = func(ctx context.Context, db *sql.DB, driver string, user repository.User) error {
			return repository.NewSQLUserRepository(db, driver).EnsureSuperAdmin(ctx, user)
		}
	}
	if dependencies.IssueIP == nil {
		dependencies.IssueIP = GenerateManagementCertificate
	}
	if dependencies.SaveConfig == nil {
		dependencies.SaveConfig = func(candidate config.Config, path string) error { return candidate.Save(path) }
	}
	if dependencies.ValidateFiles == nil {
		dependencies.ValidateFiles = func(ctx context.Context, certificatePath, fullChainPath, privateKeyPath string, expected []string) error {
			_, err := (certificate.LocalValidator{}).ValidateFiles(ctx, certificatePath, fullChainPath, privateKeyPath, expected)
			return err
		}
	}
	if dependencies.RollbackDomain == nil {
		dependencies.RollbackDomain = certificate.RollbackSavedResource
	}
	if dependencies.RollbackIP == nil {
		dependencies.RollbackIP = RollbackManagementCertificate
	}
	if dependencies.RemoveFile == nil {
		dependencies.RemoveFile = os.Remove
	}
	return &Completer{
		base: dependencies.BaseConfig, configPath: strings.TrimSpace(dependencies.ConfigPath),
		bootstrapURLPath: strings.TrimSpace(dependencies.BootstrapURLPath),
		databaseSessions: dependencies.DatabaseSessions, dnsSessions: dependencies.DNSSessions,
		credentials: dependencies.Credentials, sessions: dependencies.Sessions,
		firewall: dependencies.Firewall, certificate: dependencies.Certificate,
		openDatabase: dependencies.OpenDatabase, migrate: dependencies.Migrate,
		ensureAdmin: dependencies.EnsureAdmin, issueIP: dependencies.IssueIP,
		saveConfig: dependencies.SaveConfig, validateFiles: dependencies.ValidateFiles,
		rollbackDomain: dependencies.RollbackDomain, rollbackIP: dependencies.RollbackIP,
		removeFile: dependencies.RemoveFile, warn: dependencies.Warn,
		completed: dependencies.BaseConfig.SetupReady,
	}
}

func (completer *Completer) Complete(ctx context.Context, request CompleteRequest) (result CompleteResult, err error) {
	if completer == nil {
		return result, completeError(CompleteErrorInternal, "初始化服务尚未就绪", nil)
	}
	if !completer.mu.TryLock() {
		return result, ErrCompleteBusy
	}
	defer completer.mu.Unlock()
	if completer.completed || completer.base.SetupReady {
		return result, ErrCompleteDone
	}
	if err := completer.validateDependencies(); err != nil {
		return result, err
	}
	request = normalizeCompleteRequest(request)
	if err := validateCompleteRequest(request); err != nil {
		return result, completeError(CompleteErrorInput, "初始化参数无效", err)
	}

	databaseSession, discardDatabase, err := completer.databaseSessions.Claim(request.DatabaseVerificationToken)
	if err != nil {
		return result, completeError(CompleteErrorVerification, "数据库验证结果无效或已经过期，请重新测试", err)
	}
	var discardDNS func() error
	committed := false
	defer func() {
		if committed {
			return
		}
		if discardDNS != nil {
			completer.cleanupCredential(discardDNS, "失败初始化的 DNS 临时凭据回收未确认")
		}
		if discardDatabase != nil {
			completer.cleanupCredential(discardDatabase, "失败初始化的数据库临时凭据回收未确认")
		}
	}()

	dnsSession, discardDNS, err := completer.dnsSessions.Claim(request.DNSVerificationToken)
	if err != nil {
		return result, completeError(CompleteErrorVerification, "DNS 验证结果无效或已经过期，请重新测试", err)
	}
	if err := validateResolvedDNS(request, dnsSession); err != nil {
		return result, completeError(CompleteErrorVerification, "DNS 验证结果与管理入口不匹配，请重新验证", err)
	}

	candidate := completer.base
	candidate.Normalize()
	candidate.ACME.Environment = request.ACMEEnvironment
	if err := setup.Apply(&candidate, setup.Input{
		AdminUsername: request.AdminUsername, AdminEmail: request.AdminEmail, AdminPassword: request.AdminPassword,
		ServerPort: request.ServerPort, DNSProvider: dnsSession.ProviderID,
		DNSCredentialRef: dnsSession.CredentialRef, DNSVerified: true,
		PropagationTimeoutSecs:         request.PropagationTimeoutSecs,
		PropagationPollingIntervalSecs: request.PropagationPollingIntervalSecs,
		CertificateRoot:                candidate.Paths.Certificate,
		WebHostname:                    request.WebHostname, WebPublicIP: request.WebPublicIP,
		WebEnabled: request.WebEnabled, HTTPSMode: request.HTTPSMode,
	}); err != nil {
		return result, completeError(CompleteErrorInput, "初始化参数校验失败", err)
	}
	candidate.Database = databaseSession.Config
	if err := database.ValidateConfig(candidate.Database); err != nil {
		return result, completeError(CompleteErrorVerification, "数据库验证结果不可用于正式配置", err)
	}

	runtimeDatabase, err := database.ResolveConfig(ctx, candidate.Database, completer.credentials)
	if err != nil {
		return result, completeError(CompleteErrorDependency, "读取数据库凭据失败，请重新测试数据库连接", err)
	}
	db, err := completer.openDatabase(ctx, runtimeDatabase)
	if err != nil {
		return result, completeError(CompleteErrorDependency, "连接数据库失败，请重新检查数据库状态", err)
	}
	defer db.Close()
	if err := completer.migrate(ctx, db.DB, runtimeDatabase.Driver); err != nil {
		return result, completeError(CompleteErrorDependency, "初始化数据库结构失败", err)
	}
	if err := completer.ensureAdmin(ctx, db.DB, runtimeDatabase.Driver, repository.User{
		ID: "primary-super-admin", Username: candidate.Admin.Username, Email: candidate.Admin.Email,
		PasswordHash: candidate.Admin.PasswordHash,
	}); err != nil {
		return result, completeError(CompleteErrorDependency, "同步超级管理员失败", err)
	}

	certificateResult, err := completer.issueManagementCertificate(ctx, candidate)
	if err != nil {
		return result, err
	}
	certificateSaved := true
	var firewallResult firewall.Result
	defer func() {
		if committed {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if firewallResult.Created {
			if rollbackErr := completer.firewall.Rollback(rollbackCtx, firewallResult); rollbackErr != nil {
				err = errors.Join(err, completeError(CompleteErrorInternal, "初始化失败且防火墙规则回滚未完成", rollbackErr))
			}
		}
		if certificateSaved {
			if rollbackErr := certificateResult.rollback(rollbackCtx); rollbackErr != nil {
				err = errors.Join(err, completeError(CompleteErrorInternal, "初始化失败且管理证书回滚未完成", rollbackErr))
			}
		}
	}()

	if err := validateCompletionCertificatePaths(certificateResult); err != nil {
		return result, completeError(CompleteErrorInternal, "管理证书文件状态无效", err)
	}
	if err := completer.validateFiles(ctx, certificateResult.certificatePath, certificateResult.fullChainPath, certificateResult.privateKeyPath, []string{certificateResult.target}); err != nil {
		return result, completeError(CompleteErrorDependency, "管理证书校验失败", err)
	}
	candidate.Server.TLSCert = certificateResult.fullChainPath
	candidate.Server.TLSKey = certificateResult.privateKeyPath

	firewallResult, err = completer.firewall.OpenTCP(ctx, candidate.Server.Port)
	if err != nil {
		return result, completeError(CompleteErrorDependency, "配置主服务防火墙规则失败", err)
	}
	candidate.SetupReady = true
	candidate.Web.InitReady = true
	if err := candidate.Validate(); err != nil {
		return result, completeError(CompleteErrorInternal, "最终配置校验失败", err)
	}
	if err := ctx.Err(); err != nil {
		return result, completeError(CompleteErrorInternal, "初始化请求已取消", err)
	}
	if err := completer.saveConfig(candidate, completer.configPath); err != nil {
		return result, completeError(CompleteErrorInternal, "保存正式配置失败", err)
	}

	committed = true
	completer.completed = true
	completer.base = candidate
	result = CompleteResult{Completed: true, ManagementURL: managementURL(candidate)}
	result.Warnings = completer.afterCommit(candidate)
	return result, nil
}

type completionCertificate struct {
	target          string
	certificatePath string
	fullChainPath   string
	privateKeyPath  string
	rollback        func(context.Context) error
}

func (completer *Completer) issueManagementCertificate(ctx context.Context, candidate config.Config) (completionCertificate, error) {
	if candidate.Web.Hostname != "" {
		issueCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		saved, err := completer.certificate.IssueForSetup(issueCtx, certificate.SetupCertificateRequest{
			Domains: []string{candidate.Web.Hostname}, CertificateType: "single",
			ProviderID: candidate.DNS.Provider, CredentialRef: candidate.DNS.CredentialRef,
			AccountEmail: candidate.Admin.Email, Environment: candidate.ACME.Environment,
			CertificateRoot: candidate.Paths.Certificate, BackupRoot: candidate.Paths.Backup,
		})
		if err != nil {
			return completionCertificate{}, completeError(CompleteErrorDependency, "申请管理域名证书失败", err)
		}
		return completionCertificate{
			target: candidate.Web.Hostname, certificatePath: saved.CertificatePath,
			fullChainPath: saved.FullChainPath, privateKeyPath: saved.PrivateKeyPath,
			rollback: func(ctx context.Context) error { return completer.rollbackDomain(ctx, saved) },
		}, nil
	}
	saved, err := completer.issueIP(ctx, candidate.Paths.Certificate, candidate.Web.PublicIP)
	if err != nil {
		return completionCertificate{}, completeError(CompleteErrorDependency, "生成公网 IP 管理证书失败", err)
	}
	return completionCertificate{
		target: candidate.Web.PublicIP, certificatePath: saved.CertificatePath,
		fullChainPath: saved.FullChainPath, privateKeyPath: saved.PrivateKeyPath,
		rollback: func(ctx context.Context) error { return completer.rollbackIP(ctx, saved) },
	}, nil
}

func (completer *Completer) validateDependencies() error {
	if completer.databaseSessions == nil || completer.dnsSessions == nil || completer.credentials == nil ||
		completer.sessions == nil || completer.firewall == nil || completer.certificate == nil ||
		completer.openDatabase == nil || completer.migrate == nil || completer.ensureAdmin == nil ||
		completer.issueIP == nil || completer.saveConfig == nil || completer.validateFiles == nil ||
		completer.rollbackDomain == nil || completer.rollbackIP == nil || completer.removeFile == nil {
		return completeError(CompleteErrorInternal, "初始化服务依赖尚未就绪", nil)
	}
	if completer.configPath == "" || !filepath.IsAbs(completer.configPath) {
		return completeError(CompleteErrorInternal, "正式配置路径无效", nil)
	}
	return nil
}

func normalizeCompleteRequest(request CompleteRequest) CompleteRequest {
	request.DatabaseVerificationToken = strings.TrimSpace(request.DatabaseVerificationToken)
	request.DNSVerificationToken = strings.TrimSpace(request.DNSVerificationToken)
	request.AdminUsername = strings.TrimSpace(request.AdminUsername)
	request.AdminEmail = strings.ToLower(strings.TrimSpace(request.AdminEmail))
	request.WebHostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(request.WebHostname), "."))
	request.WebPublicIP = strings.Trim(strings.TrimSpace(request.WebPublicIP), "[]")
	request.HTTPSMode = strings.ToLower(strings.TrimSpace(request.HTTPSMode))
	request.ACMEEnvironment = strings.ToLower(strings.TrimSpace(request.ACMEEnvironment))
	if request.HTTPSMode == "" {
		request.HTTPSMode = "direct"
	}
	if request.ACMEEnvironment == "" {
		request.ACMEEnvironment = "staging"
	}
	return request
}

func validateCompleteRequest(request CompleteRequest) error {
	if request.DatabaseVerificationToken == "" || request.DNSVerificationToken == "" {
		return errors.New("必须先完成数据库和 DNS 在线验证")
	}
	if request.WebHostname == "" && request.WebPublicIP == "" {
		return errors.New("必须配置管理域名或公网 IP")
	}
	if request.WebHostname != "" && request.WebPublicIP != "" {
		return errors.New("管理域名和公网 IP 不能同时配置")
	}
	if request.WebHostname != "" && !domain.IsValidName(request.WebHostname) {
		return errors.New("管理域名格式无效")
	}
	if request.WebPublicIP != "" {
		if _, err := normalizePublicManagementIP(request.WebPublicIP); err != nil {
			return err
		}
	}
	if request.ACMEEnvironment != "staging" && request.ACMEEnvironment != "production" {
		return errors.New("ACME 环境只能是 staging 或 production")
	}
	return nil
}

func validateResolvedDNS(request CompleteRequest, session setup.DNSVerificationSession) error {
	if !strings.EqualFold(strings.TrimSpace(session.ProviderID), dnsprovider.Aliyun) {
		return errors.New("首次初始化仅支持阿里云 DNS")
	}
	testDomain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(session.TestDomain), "."))
	if err := dnsprovider.ValidateAliyunTargetDomains(session.ProviderID, []string{testDomain}); err != nil {
		return err
	}
	if request.WebHostname != "" && testDomain != request.WebHostname {
		return errors.New("DNS 验证域名必须与管理域名完全一致")
	}
	return nil
}

func validateCompletionCertificatePaths(saved completionCertificate) error {
	for _, path := range []string{saved.certificatePath, saved.fullChainPath, saved.privateKeyPath} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("管理证书路径必须是普通文件：%s", path)
		}
	}
	return nil
}

func (completer *Completer) afterCommit(candidate config.Config) []string {
	warnings := make([]string, 0, 4)
	appendWarning := func(message string) {
		warnings = append(warnings, message)
		if completer.warn != nil {
			completer.warn(message)
		}
	}
	completer.sessions.RevokeAll()
	completer.databaseSessions.DiscardAllExcept(candidate.Database.CredentialRef)
	completer.dnsSessions.DiscardAllExcept(candidate.DNS.CredentialRef)
	if completer.bootstrapURLPath != "" {
		if err := completer.removeFile(completer.bootstrapURLPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			appendWarning("正式配置已保存，但 Bootstrap 地址文件清理失败")
		}
	}
	return warnings
}

func (completer *Completer) cleanupCredential(cleanup func() error, message string) {
	if err := cleanup(); err != nil && completer.warn != nil {
		completer.warn(message)
	}
}

func managementURL(candidate config.Config) string {
	host := strings.TrimSpace(candidate.Web.Hostname)
	if host == "" {
		host = strings.TrimSpace(candidate.Web.PublicIP)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.To4() == nil {
		host = "[" + ip.String() + "]"
	}
	if candidate.Server.Port != 443 {
		host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(candidate.Server.Port))
		if strings.HasPrefix(host, "[") && candidate.Server.Port == 443 {
			host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		}
	}
	return (&url.URL{Scheme: "https", Host: host, Path: "/"}).String()
}

func completeError(kind CompleteErrorKind, message string, err error) error {
	return &CompleteError{Kind: kind, Message: message, Err: err}
}
