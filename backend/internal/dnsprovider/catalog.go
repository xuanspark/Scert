package dnsprovider

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	Cloudflare   = "cloudflare"
	Aliyun       = "aliyun"
	DNSPod       = "dnspod"
	CNAME        = "cname"
	HuaweiCloud  = "huaweicloud"
	TencentCloud = "tencentcloud"
	Route53      = "route53"
	GoogleCloud  = "googlecloud"
	Other        = "other"
)

var ErrProviderUnavailable = errors.New("DNS 服务商适配器尚未开放")

type CredentialField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
}

type Descriptor struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Available        bool              `json:"available"`
	CredentialFields []CredentialField `json:"credential_fields"`
	Description      string            `json:"description"`
}

var descriptors = map[string]Descriptor{
	Cloudflare: {
		ID: Cloudflare, Name: "Cloudflare", Available: true,
		Description: "使用 API Token 创建并清理 DNS-01 测试记录。",
		CredentialFields: []CredentialField{
			{Name: "api_token", Label: "DNS API Token", Secret: true, Required: true, Placeholder: "需要 Zone:Read 与 DNS:Edit 权限"},
			{Name: "zone_token", Label: "Zone API Token", Secret: true, Placeholder: "可选；用于拆分 Zone 读取权限"},
		},
	},
	Aliyun: {
		ID: Aliyun, Name: "阿里云 DNS", Description: "适配器字段已定义，在线验证将在后续任务接入。",
		CredentialFields: []CredentialField{
			{Name: "access_key_id", Label: "AccessKey ID", Required: true},
			{Name: "access_key_secret", Label: "AccessKey Secret", Secret: true, Required: true},
			{Name: "security_token", Label: "Security Token", Secret: true},
		},
	},
	DNSPod: {
		ID: DNSPod, Name: "DNSPod", Description: "使用腾讯云 DNSPod API 凭据；在线验证将在后续任务接入。",
		CredentialFields: []CredentialField{
			{Name: "secret_id", Label: "SecretId", Required: true},
			{Name: "secret_key", Label: "SecretKey", Secret: true, Required: true},
			{Name: "session_token", Label: "Session Token", Secret: true},
		},
	},
	CNAME: {
		ID: CNAME, Name: "CNAME 委派", Description: "需要先配置委派目标和目标 DNS 服务商，后续任务接入自动验证。",
		CredentialFields: []CredentialField{
			{Name: "delegated_domain", Label: "专用验证域", Required: true, Placeholder: "例如 acme.example.com"},
			{Name: "delegated_provider", Label: "委派目标 DNS 服务商", Required: true},
		},
	},
	HuaweiCloud: {
		ID: HuaweiCloud, Name: "华为云", Description: "适配器字段已定义，在线验证将在后续任务接入。",
		CredentialFields: []CredentialField{
			{Name: "access_key_id", Label: "Access Key ID", Required: true},
			{Name: "secret_access_key", Label: "Secret Access Key", Secret: true, Required: true},
			{Name: "region", Label: "区域", Required: true, Placeholder: "例如 cn-north-4"},
		},
	},
	TencentCloud: {
		ID: TencentCloud, Name: "腾讯云", Description: "使用腾讯云 DNSPod API；在线验证将在后续任务接入。",
		CredentialFields: []CredentialField{
			{Name: "secret_id", Label: "SecretId", Required: true},
			{Name: "secret_key", Label: "SecretKey", Secret: true, Required: true},
			{Name: "region", Label: "区域", Placeholder: "可选"},
			{Name: "session_token", Label: "Session Token", Secret: true},
		},
	},
	Route53: {
		ID: Route53, Name: "AWS Route 53", Description: "适配器字段已定义，在线验证将在后续任务接入。",
		CredentialFields: []CredentialField{
			{Name: "access_key_id", Label: "Access Key ID", Required: true},
			{Name: "secret_access_key", Label: "Secret Access Key", Secret: true, Required: true},
			{Name: "region", Label: "区域", Required: true, Placeholder: "例如 us-east-1"},
			{Name: "session_token", Label: "Session Token", Secret: true},
			{Name: "hosted_zone_id", Label: "Hosted Zone ID"},
		},
	},
	GoogleCloud: {
		ID: GoogleCloud, Name: "Google Cloud DNS", Description: "适配器字段已定义，在线验证将在后续任务接入。",
		CredentialFields: []CredentialField{
			{Name: "service_account_json", Label: "服务账号 JSON", Secret: true, Required: true},
			{Name: "project", Label: "项目 ID", Required: true},
			{Name: "zone_id", Label: "托管区域 ID"},
		},
	},
	Other: {
		ID: Other, Name: "其他服务商", Description: "扩展占位；需要实现专用 Provider 适配器后才能在线验证。",
		CredentialFields: []CredentialField{
			{Name: "provider_name", Label: "服务商标识", Required: true},
			{Name: "config_json", Label: "Provider 配置 JSON", Secret: true, Required: true},
		},
	},
}

func Descriptors() []Descriptor {
	order := []string{Cloudflare, Aliyun, DNSPod, CNAME, HuaweiCloud, TencentCloud, Route53, GoogleCloud, Other}
	result := make([]Descriptor, 0, len(order))
	for _, id := range order {
		result = append(result, descriptors[id])
	}
	return result
}

func GetDescriptor(providerID string) (Descriptor, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	descriptor, ok := descriptors[providerID]
	if !ok {
		return Descriptor{}, fmt.Errorf("未知 DNS 服务商：%s", providerID)
	}
	return descriptor, nil
}

func ValidateCredentials(providerID string, credentials map[string]string) (map[string]string, error) {
	descriptor, err := GetDescriptor(providerID)
	if err != nil {
		return nil, err
	}
	cleaned := make(map[string]string, len(descriptor.CredentialFields))
	allowed := make(map[string]struct{}, len(descriptor.CredentialFields))
	for _, field := range descriptor.CredentialFields {
		allowed[field.Name] = struct{}{}
		value := strings.TrimSpace(credentials[field.Name])
		if field.Required && value == "" {
			return nil, fmt.Errorf("%s不能为空", field.Label)
		}
		if value != "" {
			cleaned[field.Name] = value
		}
	}
	for name := range credentials {
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("DNS 凭据字段无效：%s", name)
		}
	}
	return cleaned, nil
}

func CredentialNames(providerID string) []string {
	descriptor, err := GetDescriptor(providerID)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(descriptor.CredentialFields))
	for _, field := range descriptor.CredentialFields {
		result = append(result, field.Name)
	}
	sort.Strings(result)
	return result
}
