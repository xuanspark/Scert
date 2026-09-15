package dnsprovider

import (
	"errors"
	"fmt"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"golang.org/x/net/publicsuffix"
)

const protectedAliyunDomain = "pcux.cn"

// ResolveAliyunTargetDomain 从本次输入中得到唯一可写入的注册根域。
// 它不枚举账号域名、不猜测托管区，也绝不因一次失败改写为父域或其他候选域名。
func ResolveAliyunTargetDomain(requested string) (string, error) {
	requested = normalizeAuthorizationDomain(requested)
	base := strings.TrimPrefix(requested, "*.")
	if !domain.IsValidName(requested) || base == "" {
		return "", errors.New("待操作域名格式无效")
	}
	if isProtectedAliyunDomain(base) {
		return "", fmt.Errorf("域名 %s 受本机保护策略限制，未构造阿里云客户端或调用云 API", requested)
	}
	zone, err := publicsuffix.EffectiveTLDPlusOne(base)
	if err != nil {
		return "", fmt.Errorf("待操作域名必须包含可注册根域：%s", requested)
	}
	return normalizeAuthorizationDomain(zone), nil
}

func isProtectedAliyunDomain(name string) bool {
	name = normalizeAuthorizationDomain(strings.TrimPrefix(name, "*."))
	return name == protectedAliyunDomain || strings.HasSuffix(name, "."+protectedAliyunDomain)
}

// ValidateAliyunTargetDomains 在任何凭据读取、客户端构造或云 API 调用前完成整单本地校验。
// SAN 中只要有一个保护域，整单都会被拒绝。
func ValidateAliyunTargetDomains(providerID string, domains []string) error {
	if !strings.EqualFold(strings.TrimSpace(providerID), Aliyun) {
		return nil
	}
	if len(domains) == 0 {
		return errors.New("至少需要一个待操作域名")
	}
	for _, name := range domains {
		if _, err := ResolveAliyunTargetDomain(name); err != nil {
			return err
		}
	}
	return nil
}
