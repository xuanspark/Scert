package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type dnsAccountView struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Provider          string `json:"provider"`
	ProviderName      string `json:"provider_name"`
	AccountIdentifier string `json:"account_identifier"`
	ProjectID         string `json:"project_id"`
	OwnerUserID       string `json:"owner_user_id,omitempty"`
	OwnerUsername     string `json:"owner_username"`
	OwnerState        string `json:"owner_state"`
	VerificationState string `json:"verification_state"`
	CredentialState   string `json:"credential_state"`
	Status            string `json:"status"`
	Revision          int64  `json:"revision"`
	Managed           bool   `json:"managed"`
}

// listDNSAccounts 合并独立账户和旧系统配置；不枚举秘密仓库中的临时或历史密钥。
func (s *Server) listDNSAccounts(c *gin.Context) {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	items := make([]dnsAccountView, 0, 1)
	hasLegacyAccount := false
	var legacyAccount domain.DNSAccount
	configuredLegacyProjectID := strings.TrimSpace(cfg.DNS.ProjectID)
	if configuredLegacyProjectID == "" {
		configuredLegacyProjectID = domain.DefaultProjectID
	}
	if repo, ok := s.projectMembers.(repository.DNSAccountRepository); ok {
		accounts, err := repo.ListVisibleDNSAccounts(c.Request.Context(), c.GetString(authenticatedActorKey))
		if err != nil {
			s.writeDNSAccountError(c, err)
			return
		}
		for _, account := range accounts {
			if account.ID == "legacy-system-dns" {
				if account.ProjectID != configuredLegacyProjectID {
					continue
				}
				hasLegacyAccount = true
				legacyAccount = account
				continue
			}
			item, err := s.managedDNSAccountView(c, account)
			if err != nil {
				s.writeError(c, http.StatusServiceUnavailable, "读取 DNS 账户归属失败", nil)
				return
			}
			items = append(items, item)
		}
	}
	if strings.TrimSpace(cfg.DNS.CredentialRef) == "" {
		c.JSON(http.StatusOK, gin.H{"items": items})
		return
	}
	projectID := strings.TrimSpace(cfg.DNS.ProjectID)
	if projectID == "" {
		projectID = domain.DefaultProjectID
	}
	// 在查询账户归属或解密凭据之前，按配置中的真实项目实时判权。
	// 全局管理员身份不能代替项目授权，撤权后的旧会话也不能继续读取。
	authorizer, ok := s.projectMembers.(repository.ProjectAuthorizer)
	if !ok {
		authorizer, ok = s.projectDomains.(repository.ProjectAuthorizer)
	}
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "项目 DNS 权限服务尚未就绪", nil)
		return
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), projectID, domain.PermissionDNSRead); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			// 列表只返回可见账户，不泄露其他项目是否配置了凭据。
			c.JSON(http.StatusOK, gin.H{"items": items})
			return
		}
		s.writeError(c, http.StatusServiceUnavailable, "读取项目 DNS 权限失败，请稍后重试", nil)
		return
	}
	if hasLegacyAccount {
		item, viewErr := s.configuredLegacyDNSAccountView(c, legacyAccount, cfg.DNS.Provider, cfg.DNS.CredentialRef, cfg.DNS.OwnerUserID, cfg.Admin.Username)
		if viewErr != nil {
			s.writeError(c, http.StatusServiceUnavailable, "读取 DNS 账户归属失败", nil)
			return
		}
		items = append(items, item)
		c.JSON(http.StatusOK, gin.H{"items": items})
		return
	}
	if importer, ok := s.projectMembers.(repository.DNSLegacyAccountImporter); ok {
		if managedRepo, repoOK := s.projectMembers.(repository.DNSAccountRepository); repoOK && strings.TrimSpace(cfg.DNS.Provider) != "" {
			const legacyID = "legacy-system-dns"
			legacyAccount, err := managedRepo.GetVisibleDNSAccount(c.Request.Context(), c.GetString(authenticatedActorKey), legacyID)
			if errors.Is(err, repository.ErrProjectAccessDenied) {
				legacyProjectID := strings.TrimSpace(cfg.DNS.ProjectID)
				if legacyProjectID == "" {
					legacyProjectID = domain.DefaultProjectID
				}
				legacyOwnerID := strings.TrimSpace(cfg.DNS.OwnerUserID)
				if legacyOwnerID == "" {
					legacyOwnerID = "primary-super-admin"
				}
				if err := importer.ImportLegacyDNSAccount(c.Request.Context(), domain.DNSAccount{
					ID: legacyID, Name: "旧系统 DNS 账户", ProjectID: legacyProjectID,
					OwnerUserID: legacyOwnerID, Provider: cfg.DNS.Provider, CredentialRef: cfg.DNS.CredentialRef,
				}); err != nil {
					s.writeError(c, http.StatusServiceUnavailable, "登记已配置的 DNS 账户失败，请稍后重试", nil)
					return
				}
				legacyAccount, err = managedRepo.GetVisibleDNSAccount(c.Request.Context(), c.GetString(authenticatedActorKey), legacyID)
			}
			if err == nil {
				item, viewErr := s.configuredLegacyDNSAccountView(c, legacyAccount, cfg.DNS.Provider, cfg.DNS.CredentialRef, cfg.DNS.OwnerUserID, cfg.Admin.Username)
				if viewErr != nil {
					s.writeError(c, http.StatusServiceUnavailable, "读取 DNS 账户归属失败", nil)
					return
				}
				items = append(items, item)
				c.JSON(http.StatusOK, gin.H{"items": items})
				return
			}
			if !errors.Is(err, repository.ErrProjectAccessDenied) {
				s.writeError(c, http.StatusServiceUnavailable, "读取已配置的 DNS 账户失败，请稍后重试", nil)
				return
			}
		}
	}
	item := dnsAccountView{
		ID: "system-dns", Name: "系统 DNS 账户", Provider: cfg.DNS.Provider,
		ProjectID:    projectID,
		ProviderName: "未知服务商", AccountIdentifier: "未提供账号标识",
		OwnerUserID: cfg.DNS.OwnerUserID, OwnerState: "unassigned", OwnerUsername: "未分配",
		VerificationState: cfg.DNS.VerificationState, CredentialState: "available",
		Status: domain.DNSAccountActive,
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

func (s *Server) configuredLegacyDNSAccountView(c *gin.Context, account domain.DNSAccount, provider, credentialRef, ownerUserID, adminUsername string) (dnsAccountView, error) {
	ownerID := strings.TrimSpace(ownerUserID)
	if ownerID == "" {
		ownerID = account.OwnerUserID
	}
	if ownerID == "" {
		ownerID = "primary-super-admin"
	}
	item := dnsAccountView{
		ID: account.ID, Name: account.Name, Provider: account.Provider, ProjectID: account.ProjectID,
		OwnerUserID: ownerID, OwnerState: "unassigned", OwnerUsername: "未分配",
		VerificationState: account.VerificationState, CredentialState: "available",
		Status: account.Status, Revision: account.Revision, Managed: true,
	}
	if descriptor, err := dnsprovider.GetDescriptor(provider); err == nil {
		item.ProviderName = descriptor.Name
	} else {
		item.ProviderName = "未知服务商"
	}
	if ownerID == "primary-super-admin" {
		item.OwnerState, item.OwnerUsername = "assigned", adminUsername
	} else {
		if s.users == nil {
			return dnsAccountView{}, errors.New("用户归属查询暂不可用")
		}
		owner, err := s.users.GetUserByID(c.Request.Context(), ownerID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return dnsAccountView{}, err
		}
		if errors.Is(err, sql.ErrNoRows) {
			item.OwnerState, item.OwnerUsername = "missing", "原用户已不存在"
		} else {
			item.OwnerState, item.OwnerUsername = "assigned", owner.Username
		}
	}
	credentials, err := s.readDNSCredentials(c.Request.Context(), credentialRef)
	if err != nil {
		item.CredentialState, item.AccountIdentifier = "unavailable", "暂不可读取"
	} else {
		item.AccountIdentifier = dnsAccountIdentifier(provider, credentials)
		if _, err := dnsprovider.ValidateCredentials(provider, credentials); err != nil {
			item.CredentialState = "invalid"
		}
	}
	return item, nil
}

func (s *Server) managedDNSAccountView(c *gin.Context, account domain.DNSAccount) (dnsAccountView, error) {
	item := dnsAccountView{
		ID: account.ID, Name: account.Name, Provider: account.Provider, ProviderName: "未知服务商",
		ProjectID: account.ProjectID, OwnerUserID: account.OwnerUserID,
		OwnerUsername: "原用户已不存在", OwnerState: "missing",
		VerificationState: account.VerificationState, CredentialState: "stored",
		AccountIdentifier: "凭据已隐藏", Status: account.Status, Revision: account.Revision, Managed: true,
	}
	if descriptor, err := dnsprovider.GetDescriptor(account.Provider); err == nil {
		item.ProviderName = descriptor.Name
	}
	if s.users == nil {
		return dnsAccountView{}, errors.New("用户归属查询暂不可用")
	}
	owner, err := s.users.GetUserByID(c.Request.Context(), account.OwnerUserID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dnsAccountView{}, err
	}
	if err == nil {
		item.OwnerUsername, item.OwnerState = owner.Username, "assigned"
	}
	return item, nil
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
