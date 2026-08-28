package httpapi

import (
	"context"
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
	AdminUsername                 string            `json:"admin_username"`
	AdminEmail                    string            `json:"admin_email"`
	AdminPassword                 string            `json:"admin_password"`
	ServerPort                    int               `json:"server_port"`
	EmailHTTPEnabled              *bool             `json:"email_http_enabled"`
	EmailProvider                 string            `json:"email_provider"`
	EmailEndpoint                 string            `json:"email_endpoint"`
	EmailHTTPPath                 string            `json:"email_http_path"`
	EmailHTTPLoginPath            string            `json:"email_http_login_path"`
	EmailHTTPAuthMode             string            `json:"email_http_auth_mode"`
	EmailHTTPCredentialRef        string            `json:"email_http_credential_ref"`
	EmailHTTPHeaders              map[string]string `json:"email_http_headers"`
	EmailEventMode                string            `json:"email_event_mode"`
	EmailIncludeDeploymentDetails bool              `json:"email_include_deployment_details"`
	EmailIncludeCertificateSerial bool              `json:"email_include_certificate_serial"`
	EmailIncludeManagementURL     bool              `json:"email_include_management_url"`
	EmailSenderName               string            `json:"email_sender_name"`
	EmailSenderEmail              string            `json:"email_sender_email"`
	Recipients                    []string          `json:"recipients"`
	EmailTimeoutSecs              int               `json:"email_timeout_secs"`
	EmailRetryCount               int               `json:"email_retry_count"`
	EmailRetryIntervalsSecs       []int             `json:"email_retry_intervals_secs"`
	DNSProvider                   string            `json:"dns_provider"`
	DNSCredentialRef              string            `json:"dns_credential_ref"`
	DNSVerificationToken          string            `json:"dns_verification_token"`
	DNSTestDomain                 string            `json:"dns_test_domain"`
	CertificateRoot               string            `json:"certificate_root"`
	WebHostname                   string            `json:"web_hostname"`
	WebPublicIP                   string            `json:"web_public_ip"`
	WebEnabled                    *bool             `json:"web_enabled"`
	HTTPSMode                     string            `json:"https_mode"`
}

type dnsTestRequest struct {
	ProviderID  string            `json:"provider"`
	TestDomain  string            `json:"test_domain"`
	Credentials map[string]string `json:"credentials"`
}

func (s *Server) registerSetupRoutes() {
	s.router.GET("/api/v1/setup/status", s.setupStatus)
	s.router.GET("/api/v1/setup/dns/providers", s.setupDNSProviders)
	s.router.POST("/api/v1/setup/dns/test", s.testSetupDNS)
	s.router.POST("/api/v1/setup", s.applySetup)
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
	c.JSON(http.StatusOK, gin.H{
		"initialized":            cfg.SetupReady,
		"phase":                  phase,
		"blockers":               blockers,
		"server_port":            cfg.Server.Port,
		"reset_url":              cfg.ResetURL(),
		"https_mode":             cfg.Server.HTTPSMode,
		"web_enabled":            cfg.Web.Enabled,
		"web_hostname":           cfg.Web.Hostname,
		"web_public_ip":          cfg.Web.PublicIP,
		"certificate_root":       cfg.Paths.Certificate,
		"dns_provider":           cfg.DNS.Provider,
		"dns_verification_state": cfg.DNS.VerificationState,
		"dns_test_available":     s.setupDNS != nil,
	})
}

func (s *Server) setupDNSProviders(c *gin.Context) {
	if !s.allowSetupRequest(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": dnsprovider.Descriptors()})
}

func (s *Server) testSetupDNS(c *gin.Context) {
	if !s.allowSetupRequest(c) {
		return
	}
	if s.setupDNS == nil {
		s.writeError(c, http.StatusServiceUnavailable, "DNS 在线验证服务尚未初始化", nil)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, setupRequestLimit)
	var request dnsTestRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		s.writeError(c, http.StatusBadRequest, "DNS 测试请求参数无效", err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	result, err := s.setupDNS.Verify(ctx, setup.DNSVerificationInput{
		ProviderID: request.ProviderID, TestDomain: request.TestDomain, Credentials: request.Credentials,
	})
	if err != nil {
		if errors.Is(err, dnsprovider.ErrProviderUnavailable) {
			s.writeError(c, http.StatusNotImplemented, "该 DNS 服务商在线验证适配器尚未开放", nil)
			return
		}
		if s.logger != nil {
			s.logger.Error("DNS API 在线验证失败", "provider", request.ProviderID, "test_domain", request.TestDomain)
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "DNS API 在线验证失败，请检查凭据、测试域名和 DNS 权限"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"verified":           true,
		"credential_ref":     result.CredentialRef,
		"verification_token": result.VerificationToken,
		"expires_at":         result.ExpiresAt,
		"message":            "DNS API 在线验证通过，凭据已加密保存。",
	})
}

func (s *Server) applySetup(c *gin.Context) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if !isLoopbackRequest(c.Request) {
		s.writeError(c, http.StatusForbidden, "首次初始化接口仅允许从本机访问", nil)
		return
	}
	if s.config.SetupReady {
		s.writeError(c, http.StatusConflict, "初始化已经完成，不能重复提交", nil)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, setupRequestLimit)
	var request setupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
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
		s.writeError(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	webEnabled := true
	if request.WebEnabled != nil {
		webEnabled = *request.WebEnabled
	}
	emailHTTPEnabled := false
	if request.EmailHTTPEnabled != nil {
		emailHTTPEnabled = *request.EmailHTTPEnabled
	}
	updated := s.config
	err := setup.Apply(&updated, setup.Input{
		AdminUsername:                 request.AdminUsername,
		AdminEmail:                    request.AdminEmail,
		AdminPassword:                 request.AdminPassword,
		ServerPort:                    request.ServerPort,
		EmailHTTPEnabled:              emailHTTPEnabled,
		EmailProvider:                 request.EmailProvider,
		EmailEndpoint:                 request.EmailEndpoint,
		EmailHTTPPath:                 request.EmailHTTPPath,
		EmailHTTPLoginPath:            request.EmailHTTPLoginPath,
		EmailHTTPAuthMode:             request.EmailHTTPAuthMode,
		EmailHTTPCredentialRef:        request.EmailHTTPCredentialRef,
		EmailHTTPHeaders:              request.EmailHTTPHeaders,
		EmailEventMode:                request.EmailEventMode,
		EmailIncludeDeploymentDetails: request.EmailIncludeDeploymentDetails,
		EmailIncludeCertificateSerial: request.EmailIncludeCertificateSerial,
		EmailIncludeManagementURL:     request.EmailIncludeManagementURL,
		EmailSenderName:               request.EmailSenderName,
		EmailSenderEmail:              request.EmailSenderEmail,
		EmailRecipients:               request.Recipients,
		EmailTimeoutSecs:              request.EmailTimeoutSecs,
		EmailRetryCount:               request.EmailRetryCount,
		EmailRetryIntervalsSecs:       request.EmailRetryIntervalsSecs,
		DNSProvider:                   request.DNSProvider,
		DNSCredentialRef:              request.DNSCredentialRef,
		DNSVerified:                   true,
		CertificateRoot:               request.CertificateRoot,
		WebHostname:                   request.WebHostname,
		WebPublicIP:                   request.WebPublicIP,
		WebEnabled:                    webEnabled,
		HTTPSMode:                     request.HTTPSMode,
	})
	if err != nil {
		s.writeError(c, http.StatusBadRequest, err.Error(), err)
		return
	}
	if strings.TrimSpace(s.configPath) == "" {
		s.writeError(c, http.StatusInternalServerError, "服务未配置配置文件路径，无法保存初始化结果", nil)
		return
	}
	if strings.TrimSpace(updated.Web.PublicIP) != "" {
		s.writeError(c, http.StatusBadRequest, "公网 IP 暂不支持通过 DNS-01 自动申请管理后台证书，请先配置域名或上传自有证书", nil)
		return
	}
	if s.issueSetupCertificate == nil {
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
		s.writeError(c, http.StatusBadGateway, "申请管理后台证书失败，初始化尚未完成", certificateErr)
		return
	}
	if err := validateSetupCertificateFiles(savedCertificate); err != nil {
		if rollbackErr := s.rollbackIssuedSetupCertificate(savedCertificate); rollbackErr != nil && s.logger != nil {
			s.logger.Error("管理后台证书文件校验失败，且证书回滚失败", "error", rollbackErr)
		}
		s.writeError(c, http.StatusInternalServerError, "管理后台证书文件校验失败：初始化尚未完成", err)
		return
	}
	updated.Server.TLSCert = savedCertificate.FullChainPath
	updated.Server.TLSKey = savedCertificate.PrivateKeyPath
	updated.SetupReady = true
	if err := updated.Validate(); err != nil {
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
	if err := s.setupDNS.Consume(request.DNSVerificationToken, request.DNSProvider, request.DNSCredentialRef, testDomain); err != nil && s.logger != nil {
		s.logger.Warn("初始化配置已保存，但清理 DNS 验证令牌失败", "error", err)
	}
	s.config = updated
	c.JSON(http.StatusOK, gin.H{
		"initialized": true,
		"message": fmt.Sprintf(
			"初始化配置已保存，DNS API 已完成在线验证且凭据已加密保存。%s。请确认云安全组已放行主服务端口 %d；Scert-admin 默认关闭，启动后通过 %s 使用临时重置入口。管理后台证书申请成功。",
			firewallResult.Message, updated.Server.Port, updated.ResetURL(),
		),
		"status": gin.H{
			"server_port": updated.Server.Port,
			"reset_url":   updated.ResetURL(),
			"web_enabled": updated.Web.Enabled,
			"https_mode":  updated.Server.HTTPSMode,
			"firewall":    firewallResult,
		},
	})
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
