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
	Verify(ctx context.Context, requestedDomain string, credentials map[string]string) (dnsprovider.AuthorizationReport, error)
	VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error
}

type aliyunDomainCheckRequest struct {
	Domain string `json:"domain"`
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
	// 必须先在本地拒绝受保护域，不能为无效请求读取凭据或触发云端探测。
	if err := dnsprovider.ValidateAliyunTargetDomains(provider, []string{targetDomain}); err != nil {
		c.JSON(http.StatusOK, gin.H{"verified": false, "domain": targetDomain, "message": err.Error()})
		return
	}

	credentials, err := s.readDNSCredentials(c.Request.Context(), credentialRef)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "读取阿里云 DNS 凭据失败", err)
		return
	}
	credentials, err = dnsprovider.ValidateCredentials(dnsprovider.Aliyun, credentials)
	if err != nil {
		s.writeError(c, http.StatusBadRequest, "阿里云 DNS 凭据无效", err)
		return
	}

	if s.aliyunChecker == nil {
		s.writeError(c, http.StatusNotImplemented, "阿里云权限探测器未装载", nil)
		return
	}
	report, verifyErr := s.aliyunChecker.Verify(c.Request.Context(), targetDomain, credentials)
	report = sanitizeAliyunReport(report, targetDomain)
	if verifyErr != nil {
		diagnosis := dnsprovider.DiagnoseAliyunError(verifyErr, targetDomain)
		report.Verified = false
		report.Diagnosis = diagnosis
		c.JSON(http.StatusOK, gin.H{
			"verified":       false,
			"domain":         targetDomain,
			"matched_domain": report.MatchedDomain,
			"diagnosis":      report.Diagnosis,
			"error":          diagnosis,
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

func (s *Server) validateDomainAliyunAuthorization(ctx context.Context, domainNames ...string) error {
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
	// 在读取凭据、构造阿里云客户端或发起任何云 API 请求前检查整张订单。
	if err := dnsprovider.ValidateAliyunTargetDomains(provider, domainNames); err != nil {
		return err
	}
	if s.aliyunChecker == nil {
		return errors.New("阿里云权限探测器未装载，不能确认域名授权")
	}

	credentials, err := s.readDNSCredentials(ctx, credentialRef)
	if err != nil {
		return errors.New("读取阿里云 DNS 凭据失败，不能确认域名授权，请检查受保护的凭据存储")
	}
	if _, err := dnsprovider.ValidateCredentials(dnsprovider.Aliyun, credentials); err != nil {
		return fmt.Errorf("阿里云 DNS 凭据无效，不能确认域名授权：%w", err)
	}
	for _, domainName := range domainNames {
		if err := s.aliyunChecker.VerifyDomain(ctx, dnsprovider.Aliyun, domainName, credentials); err != nil {
			diagnosis := dnsprovider.DiagnoseAliyunError(err, domainName)
			return fmt.Errorf("阿里云 RAM 账号无法操作域名 %s：%s", domainName, diagnosis)
		}
	}
	return nil
}

func sanitizeAliyunReport(report dnsprovider.AuthorizationReport, requestedDomain string) dnsprovider.AuthorizationReport {
	if report.Diagnosis != "" {
		report.Diagnosis = dnsprovider.DiagnoseAliyunError(errors.New(report.Diagnosis), requestedDomain)
	}
	report.Domains = append([]dnsprovider.AuthorizationDomain(nil), report.Domains...)
	for index := range report.Domains {
		item := &report.Domains[index]
		if item.Error == "" {
			continue
		}
		if item.Writable && !item.Cleanup {
			item.Error = "测试记录写入成功但清理失败，请检查删除权限并核对残留测试记录"
		} else {
			item.Error = dnsprovider.DiagnoseAliyunError(errors.New(item.Error), item.Domain)
		}
	}
	return report
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
