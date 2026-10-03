package certificate

import (
	"errors"
	"fmt"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func (s *Service) validateDNSIssueConfiguration(item domain.Domain, names []string) (string, error) {
	if s.credentials == nil || s.providers == nil {
		return "", errors.New("证书申请服务未初始化")
	}
	providerID := strings.ToLower(strings.TrimSpace(item.DNSProvider))
	if providerID == "" {
		providerID = strings.ToLower(strings.TrimSpace(s.config.DNS.Provider))
	}
	if providerID == "" || strings.TrimSpace(s.config.DNS.CredentialRef) == "" {
		return "", errors.New("DNS Provider 或凭据引用未配置")
	}
	// 必须在读取凭据、构造 Provider 或访问 ACME 前拒绝受保护的任意 SAN。
	if err := dnsprovider.ValidateAliyunTargetDomains(providerID, names); err != nil {
		return "", err
	}
	if configured := strings.ToLower(strings.TrimSpace(s.config.DNS.Provider)); configured != "" && providerID != configured {
		return "", fmt.Errorf("域名配置的 DNS Provider %s 尚未绑定独立凭据，当前初始化凭据属于 %s", providerID, configured)
	}
	if s.config.DNS.VerificationState != "verified" {
		return "", errors.New("DNS API 尚未通过验证")
	}
	return providerID, nil
}
