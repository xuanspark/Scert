package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type dnsAccountView struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Provider          string `json:"provider"`
	ProviderName      string `json:"provider_name"`
	AccountIdentifier string `json:"account_identifier"`
	OwnerUserID       string `json:"owner_user_id,omitempty"`
	OwnerUsername     string `json:"owner_username"`
	OwnerState        string `json:"owner_state"`
	VerificationState string `json:"verification_state"`
	CredentialState   string `json:"credential_state"`
}

// listDNSAccounts 仅列出当前绑定的配置，不枚举凭据仓库中的临时或历史密钥。
func (s *Server) listDNSAccounts(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	items := make([]dnsAccountView, 0, 1)
	if strings.TrimSpace(cfg.DNS.CredentialRef) == "" {
		c.JSON(http.StatusOK, gin.H{"items": items})
		return
	}
	item := dnsAccountView{
		ID: "system-dns", Name: "系统 DNS 账户", Provider: cfg.DNS.Provider,
		ProviderName: "未知服务商", AccountIdentifier: "未提供账号标识",
		OwnerUserID: cfg.DNS.OwnerUserID, OwnerState: "unassigned", OwnerUsername: "未分配",
		VerificationState: cfg.DNS.VerificationState, CredentialState: "available",
	}
	var owner repository.User
	ownerID := strings.TrimSpace(cfg.DNS.OwnerUserID)
	if ownerID == "" {
		// 旧版本只有一个系统 DNS 配置，没有单独的归属字段；迁移时归属初始化超级管理员。
		ownerID = "primary-super-admin"
		item.OwnerUserID = ownerID
	}
	if ownerID == "primary-super-admin" {
		owner = repository.User{ID: ownerID, Username: cfg.Admin.Username, Role: repository.UserRoleSuperAdmin}
	} else {
		if s.users == nil {
			s.writeError(c, http.StatusServiceUnavailable, "用户归属查询暂不可用", nil)
			return
		}
		var err error
		owner, err = s.users.GetUserByID(c.Request.Context(), ownerID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusServiceUnavailable, "用户归属查询暂不可用", nil)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			item.OwnerState, item.OwnerUsername = "missing", "原用户已不存在"
		}
	}
	if owner.ID != "" {
		item.OwnerState, item.OwnerUsername = "assigned", owner.Username
	}
	if descriptor, err := dnsprovider.GetDescriptor(cfg.DNS.Provider); err == nil {
		item.ProviderName = descriptor.Name
	}
	credentials, err := s.readDNSCredentials(c.Request.Context(), cfg.DNS.CredentialRef)
	if err != nil {
		item.CredentialState, item.AccountIdentifier = "unavailable", "暂不可读取"
	} else {
		item.AccountIdentifier = dnsAccountIdentifier(cfg.DNS.Provider, credentials)
		if _, err := dnsprovider.ValidateCredentials(cfg.DNS.Provider, credentials); err != nil {
			item.CredentialState = "invalid"
		}
	}
	items = append(items, item)
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// dnsAccountIdentifier 只允许输出公开标识的脱敏结果，绝不输出令牌或私钥片段。
func dnsAccountIdentifier(provider string, credentials map[string]string) string {
	var value string
	switch provider {
	case dnsprovider.Aliyun, dnsprovider.HuaweiCloud, dnsprovider.Route53:
		value = credentials["access_key_id"]
	case dnsprovider.DNSPod, dnsprovider.TencentCloud:
		value = credentials["secret_id"]
	case dnsprovider.Cloudflare:
		return "令牌账号（已隐藏）"
	case dnsprovider.GoogleCloud:
		return "服务账号（已隐藏）"
	case dnsprovider.CNAME:
		return "委派账号（已隐藏）"
	default:
		return "账号标识已隐藏"
	}
	identifier := []rune(strings.TrimSpace(value))
	if len(identifier) == 0 {
		return "未提供账号标识"
	}
	if len(identifier) <= 8 {
		return "********"
	}
	return string(identifier[:4]) + "****" + string(identifier[len(identifier)-4:])
}
