package dnsprovider

import (
	"errors"
	"fmt"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"golang.org/x/net/publicsuffix"
)

// AliyunScope 是用户授权的上限，不是从 RAM 读取结果自动推导的权限。
// 范围随凭据引用不可变地保存；修改范围必须重新在线验证并获得新的引用。
type AliyunScope struct {
	ManagedZones   []string `json:"managed_zones"`
	AllowedDomains []string `json:"allowed_domains"`
}

func ParseAliyunScope(credentials map[string]string) (AliyunScope, error) {
	var scope AliyunScope
	var err error
	scope.ManagedZones, err = parseScopeNames(credentials["managed_zones"], false)
	if err != nil {
		return scope, fmt.Errorf("请显式配置阿里云托管区：%w", err)
	}
	scope.AllowedDomains, err = parseScopeNames(credentials["allowed_domains"], true)
	if err != nil {
		return scope, fmt.Errorf("请显式配置允许操作的域名：%w", err)
	}
	for _, name := range scope.AllowedDomains {
		if scope.zoneFor(strings.TrimPrefix(name, "*.")) == "" {
			return AliyunScope{}, fmt.Errorf("允许域名 %s 不属于任何显式托管区", name)
		}
	}
	return scope, nil
}

func parseScopeNames(value string, wildcard bool) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("旧配置或空范围不允许 DNS 操作，请补充范围并重新验证")
	}
	parts := strings.Split(value, ",")
	if len(parts) > 128 {
		return nil, errors.New("每个范围最多允许 128 项")
	}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		name := normalizeAuthorizationDomain(part)
		base := strings.TrimPrefix(name, "*.")
		if !domain.IsValidName(name) || (!wildcard && name != base) {
			return nil, errors.New("范围应为逗号分隔的有效域名，托管区不能含通配符")
		}
		if _, err := publicsuffix.EffectiveTLDPlusOne(base); err != nil {
			return nil, fmt.Errorf("公共后缀不能作为操作范围：%s", name)
		}
		result = append(result, name)
	}
	return uniqueStrings(result), nil
}

func (s AliyunScope) zoneFor(name string) string {
	best := ""
	for _, zone := range s.ManagedZones {
		if (name == zone || strings.HasSuffix(name, "."+zone)) && len(zone) > len(best) {
			best = zone
		}
	}
	return best
}

// Resolve 只选择最具体的显式托管区；失败后绝不回退到父区。
// 通配授权只包含一层子域和对应通配证书，不隐含根域或多层子域。
func (s AliyunScope) Resolve(requested string) (string, error) {
	requested = normalizeAuthorizationDomain(requested)
	if !domain.IsValidName(requested) {
		return "", errors.New("待操作域名格式无效")
	}
	allowed := false
	for _, name := range s.AllowedDomains {
		if requested == name {
			allowed = true
			break
		}
		if strings.HasPrefix(name, "*.") && !strings.HasPrefix(requested, "*.") {
			suffix := name[1:]
			if strings.HasSuffix(requested, suffix) {
				label := strings.TrimSuffix(requested, suffix)
				allowed = label != "" && !strings.Contains(label, ".")
				if allowed {
					break
				}
			}
		}
	}
	zone := s.zoneFor(strings.TrimPrefix(requested, "*."))
	if !allowed || zone == "" {
		return "", fmt.Errorf("域名 %s 不在用户显式允许范围内，未调用阿里云 API", requested)
	}
	return zone, nil
}

func ValidateAliyunDomainScope(providerID string, domains []string, credentials map[string]string) error {
	if !strings.EqualFold(strings.TrimSpace(providerID), Aliyun) {
		return nil
	}
	scope, err := ParseAliyunScope(credentials)
	if err != nil {
		return err
	}
	for _, name := range domains {
		if _, err := scope.Resolve(name); err != nil {
			return err
		}
	}
	return nil
}
