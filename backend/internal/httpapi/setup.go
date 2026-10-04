package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

const setupRequestLimit = 64 * 1024

type setupDNSVerifier interface {
	Verify(ctx context.Context, input setup.DNSVerificationInput) (setup.DNSVerificationResult, error)
	Check(token, providerID, credentialRef, testDomain string) error
	Consume(token, providerID, credentialRef, testDomain string) error
}

type setupRequest struct {
	AdminUsername                  string            `json:"admin_username"`
	AdminEmail                     string            `json:"admin_email"`
	AdminPassword                  string            `json:"admin_password"`
	ServerPort                     int               `json:"server_port"`
	EmailHTTPEnabled               *bool             `json:"email_http_enabled"`
	EmailSMTPEnabled               *bool             `json:"email_smtp_enabled"`
	EmailSMTPHost                  string            `json:"email_smtp_host"`
	EmailSMTPPort                  int               `json:"email_smtp_port"`
	EmailSMTPUsername              string            `json:"email_smtp_username"`
	EmailSMTPPassword              string            `json:"email_smtp_password"`
	EmailSMTPMode                  string            `json:"email_smtp_mode"`
	EmailSMTPAllowPlaintextAuth    bool              `json:"email_smtp_allow_plaintext_auth"`
	EmailProvider                  string            `json:"email_provider"`
	EmailEndpoint                  string            `json:"email_endpoint"`
	EmailHTTPPath                  string            `json:"email_http_path"`
	EmailHTTPLoginPath             string            `json:"email_http_login_path"`
	EmailHTTPAuthMode              string            `json:"email_http_auth_mode"`
	EmailHTTPCredentialRef         string            `json:"email_http_credential_ref"`
	EmailHTTPCredentials           map[string]string `json:"email_http_credentials"`
	EmailHTTPHeaders               map[string]string `json:"email_http_headers"`
	EmailEventMode                 string            `json:"email_event_mode"`
	EmailIncludeDeploymentDetails  bool              `json:"email_include_deployment_details"`
	EmailIncludeCertificateSerial  bool              `json:"email_include_certificate_serial"`
	EmailIncludeManagementURL      bool              `json:"email_include_management_url"`
	EmailSenderName                string            `json:"email_sender_name"`
	EmailSenderEmail               string            `json:"email_sender_email"`
	Recipients                     []string          `json:"recipients"`
	EmailTimeoutSecs               int               `json:"email_timeout_secs"`
	EmailRetryCount                int               `json:"email_retry_count"`
	EmailRetryIntervalsSecs        []int             `json:"email_retry_intervals_secs"`
	DNSProvider                    string            `json:"dns_provider"`
	DNSCredentialRef               string            `json:"dns_credential_ref"`
	DNSVerificationToken           string            `json:"dns_verification_token"`
	DNSTestDomain                  string            `json:"dns_test_domain"`
	PropagationTimeoutSecs         int               `json:"propagation_timeout_secs"`
	PropagationPollingIntervalSecs int               `json:"propagation_polling_interval_secs"`
	CertificateRoot                string            `json:"certificate_root"`
	WebHostname                    string            `json:"web_hostname"`
	WebPublicIP                    string            `json:"web_public_ip"`
	WebEnabled                     *bool             `json:"web_enabled"`
	HTTPSMode                      string            `json:"https_mode"`
	ProxyHTTPSPort                 int               `json:"proxy_https_port"`
}

type dnsTestRequest struct {
	ProviderID  string            `json:"provider"`
	TestDomain  string            `json:"test_domain"`
	Credentials map[string]string `json:"credentials"`
}

func (s *Server) registerSetupRoutes() {
	s.router.GET("/api/v1/setup/status", s.setupStatus)
	// 正式服务只保留状态查询；旧写接口仅供桌面端本机初始化使用。
	if s.config.SetupReady {
		return
	}
	s.router.GET("/api/v1/setup/dns/providers", s.setupDNSProviders)
	s.router.POST("/api/v1/setup/dns/test", s.requireSameOrigin, s.requireSetupAccess, s.dnsOperation, s.testSetupDNS)
	s.router.POST("/api/v1/setup", s.requireSameOrigin, s.requireSetupAccess, s.longOperation(12*time.Minute), s.applySetup)
}

func (s *Server) setupStatus(c *gin.Context) {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !cfg.SetupReady && !isLoopbackRequest(c.Request) {
		s.writeError(c, http.StatusForbidden, "首次初始化接口仅允许从本机访问", nil)
		return
	}
	phase := "ready"
	blockers := []string{}
	if !cfg.SetupReady {
		phase = "configuration_required"
		blockers = append(blockers, "需要完成首次初始化")
		if cfg.DNS.VerificationState != "verified" {
			blockers = append(blockers, "DNS API 尚未验证")
		}
	}
	if cfg.SetupReady {
		c.JSON(http.StatusOK, gin.H{
			"initialized": cfg.SetupReady,
			"phase":       phase,
			"blockers":    blockers,
			"web_enabled": cfg.Web.Enabled,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"initialized":                       cfg.SetupReady,
		"phase":                             phase,
		"blockers":                          blockers,
		"server_port":                       cfg.Server.Port,
		"https_mode":                        cfg.Server.HTTPSMode,
		"proxy_https_port":                  cfg.Server.ProxyHTTPSPort,
		"web_enabled":                       cfg.Web.Enabled,
		"web_hostname":                      cfg.Web.Hostname,
		"web_public_ip":                     cfg.Web.PublicIP,
		"certificate_root":                  cfg.Paths.Certificate,
		"dns_provider":                      cfg.DNS.Provider,
		"dns_verification_state":            cfg.DNS.VerificationState,
		"dns_test_available":                s.setupDNS != nil,
		"propagation_timeout_secs":          cfg.ACME.PropagationTimeoutSecs,
		"propagation_polling_interval_secs": cfg.ACME.PropagationPollingIntervalSecs,
	})
}

func (s *Server) setupDNSProviders(c *gin.Context) {
	if !s.allowSetupRequest(c) {
		return
	}
	s.listDNSProviders(c)
}

func (s *Server) listAdminDNSProviders(c *gin.Context) {
	s.listDNSProviders(c)
}

func (s *Server) listDNSProviders(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"items": dnsprovider.Descriptors()})
}

func (s *Server) testSetupDNS(c *gin.Context) {
	if !s.allowSetupRequest(c) {
		return
	}
	s.testDNS(c)
}

func (s *Server) testAdminDNS(c *gin.Context) {
	s.testDNS(c)
}

func (s *Server) testDNS(c *gin.Context) {
	verificationTimeoutSecs := int(dnsprovider.PermissionVerificationTimeout.Seconds())
	if s.setupDNS == nil {
		s.writeDNSTestError(c, http.StatusServiceUnavailable, "DNS 在线验证服务尚未初始化", nil, verificationTimeoutSecs)
		return
	}
	var request dnsTestRequest
	if err := bindJSON(c, &request, setupRequestLimit); err != nil {
		s.writeDNSTestError(c, http.StatusBadRequest, "DNS 测试请求参数无效", err, verificationTimeoutSecs)
		return
	}
	verificationTimeoutSecs = int(dnsAPIPermissionVerificationTimeoutFor(request.ProviderID).Seconds())
	ctx, cancel := context.WithTimeout(c.Request.Context(), dnsAPIPermissionVerificationTimeoutFor(request.ProviderID))
	defer cancel()
	result, err := s.setupDNS.Verify(ctx, setup.DNSVerificationInput{
		ProviderID: request.ProviderID, TestDomain: request.TestDomain, Credentials: request.Credentials,
	})
	if err != nil {
		if errors.Is(err, dnsprovider.ErrProviderUnavailable) {
			c.JSON(http.StatusNotImplemented, gin.H{
				"error":                     "该 DNS 服务商在线验证适配器尚未开放",
				"verification_timeout_secs": verificationTimeoutSecs,
			})
			return
		}
		report := result.TestReport
		var verificationErr *setup.DNSVerificationError
		if errors.As(err, &verificationErr) {
			report = verificationErr.Report
		}
		if s.logger != nil {
			s.logger.Error("DNS API 在线验证失败", "provider", request.ProviderID, "test_domain", request.TestDomain)
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":                     "DNS API 在线验证失败，请检查凭据、测试域名和 DNS 权限",
			"test_report":               report,
			"verification_timeout_secs": verificationTimeoutSecs,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"verified":                  true,
		"credential_ref":            result.CredentialRef,
		"verification_token":        result.VerificationToken,
		"expires_at":                result.ExpiresAt,
		"test_report":               result.TestReport,
		"verification_timeout_secs": verificationTimeoutSecs,
		"message":                   "DNS API 在线验证通过，凭据已加密保存。",
	})
}

func (s *Server) writeDNSTestError(c *gin.Context, status int, message string, err error, verificationTimeoutSecs int) {
	if s.logger != nil {
		s.logger.Error(message, "error", err)
	}
	c.JSON(status, gin.H{
		"error":                     message,
		"verification_timeout_secs": verificationTimeoutSecs,
	})
}

func dnsAPIPermissionVerificationTimeout() time.Duration {
	return dnsprovider.PermissionVerificationTimeout
}

func dnsAPIPermissionVerificationTimeoutFor(providerID string) time.Duration {
	return dnsprovider.PermissionVerificationTimeoutFor(providerID)
}

func (s *Server) applySetup(c *gin.Context) {
	if !s.setupMu.TryLock() {
		s.writeError(c, http.StatusConflict, "初始化正在执行，请等待当前操作完成", nil)
		return
	}
	defer s.setupMu.Unlock()
	if !s.tlsMu.TryLock() {
		s.writeError(c, http.StatusConflict, "管理证书或配置正在更新，请稍后重试", nil)
		return
	}
	defer s.tlsMu.Unlock()
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if !isLoopbackRequest(c.Request) {
		s.writeError(c, http.StatusForbidden, "首次初始化接口仅允许从本机访问", nil)
		return
	}
	if cfg.SetupReady {
		s.writeError(c, http.StatusConflict, "初始化已经完成，不能重复提交", nil)
		return
	}
	var request setupRequest
	if err := bindJSON(c, &request, setupRequestLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "初始化请求参数无效", err)
		return
	}
	if s.setupDNS == nil {
		s.writeError(c, http.StatusServiceUnavailable, "DNS 在线验证服务尚未初始化", nil)
		return
	}
	testDomain := strings.TrimSpace(request.DNSTestDomain)
	if testDomain == "" {
		testDomain = strings.TrimSpace(request.WebHostname)
	}
	if err := s.setupDNS.Check(request.DNSVerificationToken, request.DNSProvider, request.DNSCredentialRef, testDomain); err != nil {
		s.writeError(c, http.StatusBadRequest, "DNS 验证状态无效，请重新完成在线测试", err)
		return
	}
	dnsCredentialRetained := false
	defer func() {
		if dnsCredentialRetained || s.secrets == nil {
			return
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.secrets.Delete(cleanupContext, strings.TrimSpace(request.DNSCredentialRef)); err != nil && s.logger != nil {
			s.logger.Warn("\u521d\u59cb\u5316\u5931\u8d25\u540e\u6e05\u7406 DNS \u51ed\u6362\u5931\u8d25", "error", err)
		}
	}()
	webEnabled := true
	if request.WebEnabled != nil {
		webEnabled = *request.WebEnabled
	}
	emailHTTPEnabled := false
	if request.EmailHTTPEnabled != nil {
		emailHTTPEnabled = *request.EmailHTTPEnabled
	}
	emailSMTPEnabled := false
	if request.EmailSMTPEnabled != nil {
		emailSMTPEnabled = *request.EmailSMTPEnabled
	}
	createdCredentialRefs := make([]string, 0, 2)
	emailCredentialRef := strings.TrimSpace(request.EmailHTTPCredentialRef)
	smtpCredentialRef := ""
	if emailSMTPEnabled {
		smtpCredentialRef = strings.TrimSpace(cfg.Notification.SMTPCredentialRef)
		if strings.TrimSpace(request.EmailSMTPPassword) != "" {
			if strings.TrimSpace(request.EmailSMTPUsername) == "" {
				s.writeError(c, http.StatusBadRequest, "填写 SMTP 密码前必须配置 SMTP 用户名", nil)
				return
			}
			if s.secrets == nil {
				s.writeError(c, http.StatusServiceUnavailable, "SMTP 通知敏感凭据存储尚未初始化", nil)
				return
			}
			var smtpErr error
			smtpCredentialRef, smtpErr = newNotificationCredentialReference()
			if smtpErr != nil {
				s.writeError(c, http.StatusInternalServerError, "生成 SMTP 通知凭据引用失败", smtpErr)
				return
			}
			createdCredentialRefs = append(createdCredentialRefs, smtpCredentialRef)
		}
		if smtpCredentialRef == "" {
			s.writeError(c, http.StatusBadRequest, "启用 SMTP 通知后必须填写 SMTP 密码或使用已有凭据", nil)
			return
		}
	}
	var notificationCredentials map[string]string
	var credentialErr error
	if emailHTTPEnabled && strings.ToLower(strings.TrimSpace(request.EmailHTTPAuthMode)) != "none" {
		if len(request.EmailHTTPCredentials) > 0 {
			notificationCredentials, credentialErr = normalizeNotificationCredentials(request.EmailHTTPAuthMode, request.EmailHTTPCredentials)
			if credentialErr != nil {
				s.writeError(c, http.StatusBadRequest, credentialErr.Error(), nil)
				return
			}
			if s.secrets == nil {
				s.writeError(c, http.StatusServiceUnavailable, "HTTP 通知敏感凭据存储尚未初始化", nil)
				return
			}
			emailCredentialRef, credentialErr = newNotificationCredentialReference()
			if credentialErr != nil {
				s.writeError(c, http.StatusInternalServerError, "生成 HTTP 通知凭据引用失败", credentialErr)
				return
			}
			createdCredentialRefs = append(createdCredentialRefs, emailCredentialRef)
		} else if emailCredentialRef == "" {
			s.writeError(c, http.StatusBadRequest, "启用 HTTP 通知认证后必须填写明文凭据或已有凭据引用", nil)
			return
		}
	}
	updated := cfg
	err := setup.Apply(&updated, setup.Input{
		AdminUsername:                  request.AdminUsername,
		AdminEmail:                     request.AdminEmail,
		AdminPassword:                  request.AdminPassword,
		ServerPort:                     request.ServerPort,
		EmailHTTPEnabled:               emailHTTPEnabled,
		EmailSMTPEnabled:               emailSMTPEnabled,
		EmailSMTPHost:                  request.EmailSMTPHost,
		EmailSMTPPort:                  request.EmailSMTPPort,
		EmailSMTPUsername:              request.EmailSMTPUsername,
		EmailSMTPCredentialRef:         smtpCredentialRef,
		EmailSMTPMode:                  request.EmailSMTPMode,
		EmailSMTPAllowPlaintextAuth:    request.EmailSMTPAllowPlaintextAuth,
		EmailProvider:                  request.EmailProvider,
		EmailEndpoint:                  request.EmailEndpoint,
		EmailHTTPPath:                  request.EmailHTTPPath,
		EmailHTTPLoginPath:             request.EmailHTTPLoginPath,
		EmailHTTPAuthMode:              request.EmailHTTPAuthMode,
		EmailHTTPCredentialRef:         emailCredentialRef,
		EmailHTTPHeaders:               request.EmailHTTPHeaders,
		EmailEventMode:                 request.EmailEventMode,
		EmailIncludeDeploymentDetails:  request.EmailIncludeDeploymentDetails,
		EmailIncludeCertificateSerial:  request.EmailIncludeCertificateSerial,
		EmailIncludeManagementURL:      request.EmailIncludeManagementURL,
		EmailSenderName:                request.EmailSenderName,
		EmailSenderEmail:               request.EmailSenderEmail,
		EmailRecipients:                request.Recipients,
		EmailTimeoutSecs:               request.EmailTimeoutSecs,
		EmailRetryCount:                request.EmailRetryCount,
		EmailRetryIntervalsSecs:        request.EmailRetryIntervalsSecs,
		DNSProvider:                    request.DNSProvider,
		DNSCredentialRef:               request.DNSCredentialRef,
		DNSVerified:                    true,
		PropagationTimeoutSecs:         request.PropagationTimeoutSecs,
		PropagationPollingIntervalSecs: request.PropagationPollingIntervalSecs,
		CertificateRoot:                request.CertificateRoot,
		WebHostname:                    request.WebHostname,
		WebPublicIP:                    request.WebPublicIP,
		WebEnabled:                     webEnabled,
		HTTPSMode:                      request.HTTPSMode,
		ProxyHTTPSPort:                 request.ProxyHTTPSPort,
	})
	if err != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	if strings.TrimSpace(s.configPath) == "" {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.writeError(c, http.StatusInternalServerError, "服务未配置配置文件路径，无法保存初始化结果", nil)
		return
	}
	if strings.TrimSpace(updated.Web.PublicIP) != "" {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.writeError(c, http.StatusBadRequest, "公网 IP 暂不支持通过 DNS-01 自动申请管理后台证书，请先配置域名或上传自有证书", nil)
		return
	}
	if s.issueSetupCertificate == nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.writeError(c, http.StatusServiceUnavailable, "初始化证书申请服务尚未配置", nil)
		return
	}
	updated.SetupReady = false
	certificateContext, certificateCancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	savedCertificate, certificateErr := s.issueSetupCertificate(certificateContext, certificate.SetupCertificateRequest{
		Domains:         []string{updated.Web.Hostname},
		CertificateType: "single",
		ProviderID:      updated.DNS.Provider,
		CredentialRef:   updated.DNS.CredentialRef,
		AccountEmail:    updated.Admin.Email,
		Environment:     updated.ACME.Environment,
		CertificateRoot: updated.Paths.Certificate,
		BackupRoot:      updated.Paths.Backup,
	})
	certificateCancel()
	if certificateErr != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.writeError(c, http.StatusBadGateway, "申请管理后台证书失败，初始化尚未完成", certificateErr)
		return
	}
	if err := validateSetupCertificateFiles(savedCertificate); err != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		if rollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate); rollbackErr != nil && s.logger != nil {
			s.logger.Error("管理后台证书文件校验失败，且证书回滚失败", "error", rollbackErr)
		}
		s.writeError(c, http.StatusInternalServerError, "管理后台证书文件校验失败：初始化尚未完成", err)
		return
	}
	updated.Server.TLSCert = savedCertificate.FullChainPath
	updated.Server.TLSKey = savedCertificate.PrivateKeyPath
	if len(notificationCredentials) > 0 {
		if err := s.secrets.Put(c.Request.Context(), emailCredentialRef, notificationCredentials); err != nil {
			s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
			if rollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate); rollbackErr != nil && s.logger != nil {
				s.logger.Error("保存 HTTP 通知凭据失败，且证书回滚失败", "error", rollbackErr)
			}
			s.writeError(c, http.StatusInternalServerError, "保存 HTTP 通知凭据失败，初始化尚未完成", err)
			return
		}
	}
	if emailSMTPEnabled && strings.TrimSpace(request.EmailSMTPPassword) != "" {
		if err := s.secrets.Put(c.Request.Context(), smtpCredentialRef, map[string]string{
			"username": strings.TrimSpace(request.EmailSMTPUsername),
			"password": request.EmailSMTPPassword,
		}); err != nil {
			s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
			if rollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate); rollbackErr != nil && s.logger != nil {
				s.logger.Error("保存 SMTP 通知凭据失败，且证书回滚失败", "error", rollbackErr)
			}
			s.writeError(c, http.StatusInternalServerError, "保存 SMTP 通知凭据失败，初始化尚未完成", err)
			return
		}
	}
	updated.SetupReady = true
	updated.Web.InitReady = true
	if err := updated.Validate(); err != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		if rollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate); rollbackErr != nil && s.logger != nil {
			s.logger.Error("初始化证书配置无效，且证书回滚失败", "error", rollbackErr)
		}
		s.writeError(c, http.StatusInternalServerError, "初始化证书配置无效：初始化尚未完成", err)
		return
	}
	firewallResult := firewall.Result{
		Backend: firewall.BackendNone, Port: updated.Server.Port,
		Message: "未配置防火墙管理器，已跳过本机规则创建",
	}
	if s.firewall != nil {
		firewallContext, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		firewallResult, err = s.firewall.OpenTCP(firewallContext, updated.Server.Port)
		cancel()
		if err != nil {
			s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
			if rollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate); rollbackErr != nil && s.logger != nil {
				s.logger.Error("自动配置本机防火墙失败，且证书回滚失败", "error", rollbackErr)
			}
			s.writeError(c, http.StatusInternalServerError, "自动配置本机防火墙失败，请使用管理员权限重新运行初始化程序", err)
			return
		}
	}
	if err := updated.Save(s.configPath); err != nil {
		firewallRollbackErr := error(nil)
		if firewallResult.Created && s.firewall != nil {
			rollbackContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			firewallRollbackErr = s.firewall.Rollback(rollbackContext, firewallResult)
			cancel()
			if firewallRollbackErr != nil && s.logger != nil {
				s.logger.Error("保存初始化配置失败，且回滚本机防火墙规则失败", "error", firewallRollbackErr, "rule", firewallResult.RuleName)
			}
		}
		certificateRollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate)
		if certificateRollbackErr != nil && s.logger != nil {
			s.logger.Error("保存初始化配置失败，且初始化证书回滚失败", "error", certificateRollbackErr)
		}
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		if firewallRollbackErr != nil {
			s.writeError(c, http.StatusInternalServerError, "保存初始化配置失败，且自动创建的防火墙规则回滚失败，请手动检查本机防火墙", err)
			return
		}
		if certificateRollbackErr != nil {
			s.writeError(c, http.StatusInternalServerError, "保存初始化配置失败，且管理后台证书回滚失败，请手动检查证书目录", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "保存初始化配置失败", err)
		return
	}
	dnsCredentialRetained = true
	if err := s.setupDNS.Consume(request.DNSVerificationToken, request.DNSProvider, request.DNSCredentialRef, testDomain); err != nil && s.logger != nil {
		s.logger.Warn("初始化配置已保存，但清理 DNS 验证令牌失败", "error", err)
	}
	s.configMu.Lock()
	s.config = updated
	s.configMu.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"initialized": true,
		"message": fmt.Sprintf(
			"初始化配置已保存，DNS API 已完成在线验证且凭据已加密保存。%s。请确认云安全组已放行主服务端口 %d；超级管理员密码重置不开放公网入口，请由运维人员在部署主机本机受保护终端处理。管理后台证书申请成功。",
			firewallResult.Message, updated.Server.Port,
		),
		"status": gin.H{
			"server_port": updated.Server.Port,
			"web_enabled": updated.Web.Enabled,
			"https_mode":  updated.Server.HTTPSMode,
			"firewall":    firewallResult,
		},
	})
}

func (s *Server) rollbackSetupCredentialsWithLog(references []string) {
	if err := s.rollbackSetupCredentials(references); err != nil && s.logger != nil {
		s.logger.Error("\u521d\u59cb\u5316\u654f\u611f\u51ed\u636e\u56de\u6eda\u5931\u8d25", "error", err)
	}
}

func (s *Server) rollbackSetupCredentials(references []string) error {
	if s == nil || s.secrets == nil || len(references) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seen := make(map[string]struct{}, len(references))
	var rollbackErrors []error
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			continue
		}
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}
		if err := s.secrets.Delete(ctx, reference); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("\u56de\u6eda\u654f\u611f\u51ed\u636e %s \u5931\u8d25\uFF1A%w", reference, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func (s *Server) rollbackIssuedSetupCertificate(saved certificate.SavedResource) error {
	if s.rollbackSetupCertificate == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.rollbackSetupCertificate(ctx, saved)
}
func validateSetupCertificateFiles(saved certificate.SavedResource) error {
	for label, path := range map[string]string{
		"完整证书链": saved.FullChainPath,
		"私钥":    saved.PrivateKeyPath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s路径为空", label)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("读取%s失败：%w", label, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s不是普通文件", label)
		}
	}
	return nil
}

func (s *Server) allowSetupRequest(c *gin.Context) bool {
	s.configMu.RLock()
	initialized := s.config.SetupReady
	s.configMu.RUnlock()
	if initialized {
		s.writeError(c, http.StatusConflict, "初始化已经完成，不能重复执行初始化 DNS 测试", nil)
		return false
	}
	if !isLoopbackRequest(c.Request) {
		s.writeError(c, http.StatusForbidden, "首次初始化接口仅允许从本机访问", nil)
		return false
	}
	return true
}

func isLoopbackRequest(request *http.Request) bool {
	if request == nil {
		return false
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func newNotificationCredentialReference() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "secret://notification/" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func normalizeNotificationCredentials(authMode string, values map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("HTTP 通知凭据不能为空")
	}
	switch strings.ToLower(strings.TrimSpace(authMode)) {
	case "bearer":
		if strings.TrimSpace(result["token"]) == "" && strings.TrimSpace(result["access_token"]) == "" {
			return nil, fmt.Errorf("Bearer Token 凭据必须包含 token 或 access_token")
		}
	case "hmac_sha256":
		if strings.TrimSpace(result["secret"]) == "" && strings.TrimSpace(result["hmac_secret"]) == "" && strings.TrimSpace(result["key"]) == "" && strings.TrimSpace(result["token"]) == "" {
			return nil, fmt.Errorf("HMAC-SHA256 凭据必须包含签名密钥")
		}
	case "session":
		account := result["account"]
		if account == "" {
			account = result["username"]
		}
		if account == "" {
			account = result["identifier"]
		}
		if strings.TrimSpace(account) == "" || result["password"] == "" {
			return nil, fmt.Errorf("会话认证凭据必须包含账号和密码")
		}
	default:
		return nil, fmt.Errorf("HTTP 通知认证方式无效")
	}
	return result, nil
}
