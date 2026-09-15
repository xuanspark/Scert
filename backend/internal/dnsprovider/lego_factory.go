package dnsprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/providers/dns/gcloud"
	"github.com/go-acme/lego/v5/providers/dns/huaweicloud"
	"github.com/go-acme/lego/v5/providers/dns/route53"
	"github.com/go-acme/lego/v5/providers/dns/tencentcloud"
	"golang.org/x/oauth2/google"
	gdns "google.golang.org/api/dns/v1"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type LegoFactory struct{}

func NewLegoFactory() LegoFactory {
	return LegoFactory{}
}

func (LegoFactory) Create(providerID string, credentials map[string]string) (challenge.Provider, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	descriptor, err := GetDescriptor(providerID)
	if err != nil {
		return nil, err
	}
	if !descriptor.Available {
		return nil, fmt.Errorf("%w：%s", ErrProviderUnavailable, descriptor.Name)
	}
	cleaned, err := ValidateCredentials(providerID, credentials)
	if err != nil {
		return nil, err
	}
	switch providerID {
	case CNAME:
		delegatedDomain := normalizeProviderDomain(cleaned["delegated_domain"])
		if delegatedDomain == "" || strings.HasPrefix(delegatedDomain, "*.") || !domain.IsValidName(delegatedDomain) {
			return nil, ErrInvalidDelegatedDomain
		}
		delegatedProvider := strings.ToLower(strings.TrimSpace(cleaned["delegated_provider"]))
		if delegatedProvider == CNAME || delegatedProvider == Other || delegatedProvider == "" {
			return nil, fmt.Errorf("%w: %s", ErrDelegatedProviderUnavailable, delegatedProvider)
		}
		var delegatedCredentials map[string]string
		if err := json.Unmarshal([]byte(cleaned["delegated_credentials_json"]), &delegatedCredentials); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDelegatedCredentials, err)
		}
		if delegatedCredentials == nil {
			return nil, ErrInvalidDelegatedCredentials
		}
		target, err := (LegoFactory{}).Create(delegatedProvider, delegatedCredentials)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDelegatedProviderUnavailable, err)
		}
		return newCNAMEDelegationProvider(delegatedDomain, target)
	case Cloudflare:
		cfg := cloudflare.NewDefaultConfig()
		cfg.AuthToken = cleaned["api_token"]
		cfg.ZoneToken = cleaned["zone_token"]
		if cfg.ZoneToken == "" {
			cfg.ZoneToken = cfg.AuthToken
		}
		provider, err := cloudflare.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建 Cloudflare DNS Provider 失败：%w", err)
		}
		return provider, nil
	case Aliyun:
		return newScopedAliyunProvider(cleaned)
	case DNSPod, TencentCloud:
		cfg := tencentcloud.NewDefaultConfig()
		cfg.SecretID = cleaned["secret_id"]
		cfg.SecretKey = cleaned["secret_key"]
		cfg.SessionToken = cleaned["session_token"]
		provider, err := tencentcloud.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建腾讯云 DNSPod Provider 失败：%w", err)
		}
		return provider, nil
	case HuaweiCloud:
		cfg := huaweicloud.NewDefaultConfig()
		cfg.AccessKeyID = cleaned["access_key_id"]
		cfg.SecretAccessKey = cleaned["secret_access_key"]
		cfg.Region = cleaned["region"]
		provider, err := huaweicloud.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建华为云 DNS Provider 失败：%w", err)
		}
		return provider, nil
	case Route53:
		cfg := route53.NewDefaultConfig()
		cfg.AccessKeyID = cleaned["access_key_id"]
		cfg.SecretAccessKey = cleaned["secret_access_key"]
		cfg.SessionToken = cleaned["session_token"]
		cfg.Region = cleaned["region"]
		cfg.HostedZoneID = cleaned["hosted_zone_id"]
		provider, err := route53.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建 AWS Route 53 DNS Provider 失败：%w", err)
		}
		return provider, nil
	case GoogleCloud:
		serviceAccountJSON := strings.TrimSpace(cleaned["service_account_json"])
		var serviceAccount struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal([]byte(serviceAccountJSON), &serviceAccount); err != nil {
			return nil, fmt.Errorf("创建 Google Cloud DNS Provider 失败：服务账号 JSON 格式无效：%w", err)
		}
		if serviceAccount.ProjectID == "" {
			serviceAccount.ProjectID = cleaned["project"]
			if serviceAccount.ProjectID == "" {
				return nil, fmt.Errorf("创建 Google Cloud DNS Provider 失败：项目 ID 不能为空")
			}
			var raw map[string]any
			if err := json.Unmarshal([]byte(serviceAccountJSON), &raw); err != nil {
				return nil, fmt.Errorf("创建 Google Cloud DNS Provider 失败：服务账号 JSON 格式无效：%w", err)
			}
			raw["project_id"] = serviceAccount.ProjectID
			data, err := json.Marshal(raw)
			if err != nil {
				return nil, fmt.Errorf("创建 Google Cloud DNS Provider 失败：编码服务账号 JSON 失败：%w", err)
			}
			serviceAccountJSON = string(data)
		}
		jwtConfig, err := google.JWTConfigFromJSON([]byte(serviceAccountJSON), gdns.NdevClouddnsReadwriteScope)
		if err != nil {
			return nil, fmt.Errorf("创建 Google Cloud DNS Provider 失败：服务账号凭据无效：%w", err)
		}
		cfg := gcloud.NewDefaultConfig()
		cfg.Project = serviceAccount.ProjectID
		cfg.ZoneID = cleaned["zone_id"]
		cfg.HTTPClient = jwtConfig.Client(context.Background())
		provider, err := gcloud.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建 Google Cloud DNS Provider 失败：%w", err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("%w：%s", ErrProviderUnavailable, descriptor.Name)
	}
}
