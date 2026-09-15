package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type aliyunScopeChecker interface {
	Discover(ctx context.Context, credentials map[string]string) (dnsprovider.AuthorizationReport, error)
	Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (dnsprovider.AuthorizationReport, error)
	VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error
}

type aliyunDomainCheckRequest struct {
	Domain string `json:"domain"`
}

type aliyunScopeResponse struct {
	Provider                 string                            `json:"provider"`
	Configured               bool                              `json:"configured"`
	CredentialRef            string                            `json:"credential_ref,omitempty"`
	PolicyMode               string                            `json:"policy_mode"`
	GlobalDiscoveryAvailable bool                              `json:"global_discovery_available"`
	Message                  string                            `json:"message"`
	DiscoveredDomains        []dnsprovider.AuthorizationDomain `json:"discovered_domains,omitempty"`
	ManagedDomains           []string                          `json:"managed_domains"`
	CheckedAt                string                            `json:"checked_at"`
}

func (s *Server) getAliyunDNSScope(c *gin.Context) {
	s.configMu.RLock()
	provider := strings.ToLower(strings.TrimSpace(s.config.DNS.Provider))
	credentialRef := strings.TrimSpace(s.config.DNS.CredentialRef)
	s.configMu.RUnlock()

	if provider != dnsprovider.Aliyun {
		c.JSON(http.StatusOK, aliyunScopeResponse{
			Provider:   provider,
			Configured: false,
			PolicyMode: "not_aliyun",
			Message:    "系统当前未将阿里云配置为主 DNS 服务商",
		})
		return
	}

	credentials, err := s.readDNSCredentials(c.Request.Context(), credentialRef)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "读取阿里云 DNS 凭据失败", err)
		return
	}

	response := aliyunScopeResponse{
		Provider:      dnsprovider.Aliyun,
		Configured:    true,
		CredentialRef: credentialRef,
		CheckedAt:     s.now().UTC().Format("2006-01-02T15:04:05Z"),
	}

	// 读取当前系统内已经纳管的域名列表
	if s.repository != nil {
		if items, listErr := s.repository.List(c.Request.Context()); listErr == nil {
			for _, item := range items {
				response.ManagedDomains = append(response.ManagedDomains, item.Name)
			}
		}
	}

	if s.aliyunChecker == nil {
		response.PolicyMode = "uninitialized"
		response.Message = "阿里云权限探测器未装载"
		c.JSON(http.StatusOK, response)
		return
	}

	// 默认采用资源受限安全模式，不盲目调用全局 DescribeDomains 接口，避免产生 RAM 未授权异常告警
	// 仅在显式请求参数 discover=true 时尝试全局探测
	if c.Query("discover") == "true" {
		report, discoverErr := s.aliyunChecker.Discover(c.Request.Context(), credentials)
		if discoverErr == nil && len(report.Domains) > 0 {
			response.GlobalDiscoveryAvailable = true
			response.PolicyMode = "broad_or_read_all"
			response.Message = "当前 RAM 账号具备全局根域名读取权限"
			response.DiscoveredDomains = report.Domains
		} else {
			response.GlobalDiscoveryAvailable = false
			response.PolicyMode = "resource_scoped"
			response.Message = "当前 RAM 账号受特定域名资源限制策略（最小权限原则），不支持全局枚举，已启用单域名靶向权威考证"
		}
	} else {
		response.GlobalDiscoveryAvailable = false
		response.PolicyMode = "resource_scoped"
		response.Message = "系统当前遵循 RAM 最小权限安全原则，采用单域名靶向精准考证"
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) checkAliyunDNSDomain(c *gin.Context) {
	var request aliyunDomainCheckRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "请求参数无效", err)
		return
	}
	targetDomain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(request.Domain), "."))
	if !domain.IsValidName(targetDomain) {
		s.writeError(c, http.StatusBadRequest, fmt.Sprintf("域名格式无效：%s", targetDomain), nil)
		return
	}

	s.configMu.RLock()
	provider := strings.ToLower(strings.TrimSpace(s.config.DNS.Provider))
	credentialRef := strings.TrimSpace(s.config.DNS.CredentialRef)
	s.configMu.RUnlock()

	if provider != dnsprovider.Aliyun {
		s.writeError(c, http.StatusBadRequest, "当前系统 DNS 服务商不是阿里云，无法进行阿里云 RAM 权限考证", nil)
		return
	}

	credentials, err := s.readDNSCredentials(c.Request.Context(), credentialRef)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "读取阿里云 DNS 凭据失败", err)
		return
	}

	if s.aliyunChecker == nil {
		s.writeError(c, http.StatusNotImplemented, "阿里云权限探测器未装载", nil)
		return
	}

	report, verifyErr := s.aliyunChecker.Verify(c.Request.Context(), targetDomain, credentials)
	if verifyErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"verified":       false,
			"domain":         targetDomain,
			"matched_domain": report.MatchedDomain,
			"diagnosis":      report.Diagnosis,
			"error":          verifyErr.Error(),
			"report":         report,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"verified":       true,
		"domain":         targetDomain,
		"matched_domain": report.MatchedDomain,
		"message":        "阿里云 RAM 账号对该域名的 DNS TXT 记录写入与清理权限验证通过",
		"report":         report,
	})
}

func (s *Server) validateDomainAliyunAuthorization(ctx context.Context, domainName string) error {
	if s == nil {
		return errors.New("域名授权检查服务未初始化")
	}
	s.configMu.RLock()
	setupReady := s.config.SetupReady
	provider := strings.ToLower(strings.TrimSpace(s.config.DNS.Provider))
	credentialRef := strings.TrimSpace(s.config.DNS.CredentialRef)
	s.configMu.RUnlock()

	if provider != dnsprovider.Aliyun {
		return nil
	}
	if !setupReady {
		return errors.New("系统初始化尚未完成，不能验证域名授权")
	}
	if s.aliyunChecker == nil {
		return errors.New("阿里云权限探测器未装载，不能确认域名授权")
	}

	credentials, err := s.readDNSCredentials(ctx, credentialRef)
	if err != nil {
		return fmt.Errorf("读取阿里云 DNS 凭据失败，不能确认域名授权：%w", err)
	}
	if _, err := dnsprovider.ValidateCredentials(dnsprovider.Aliyun, credentials); err != nil {
		return fmt.Errorf("阿里云 DNS 凭据无效，不能确认域名授权：%w", err)
	}

	if err := s.aliyunChecker.VerifyDomain(ctx, dnsprovider.Aliyun, domainName, credentials); err != nil {
		diagnosis := dnsprovider.DiagnoseAliyunError(err, domainName)
		return fmt.Errorf("阿里云 RAM 账号无法操作域名 %s：%s", domainName, diagnosis)
	}
	return nil
}

func (s *Server) readDNSCredentials(ctx context.Context, credentialRef string) (map[string]string, error) {
	if s == nil || strings.TrimSpace(credentialRef) == "" {
		return nil, errors.New("DNS 凭据引用未配置")
	}
	reader, ok := s.secrets.(readinessSecretReader)
	if !ok || reader == nil {
		return nil, errors.New("凭据存储未就绪")
	}
	return reader.Get(ctx, credentialRef)
}
