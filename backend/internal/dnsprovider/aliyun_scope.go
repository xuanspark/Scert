package dnsprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"github.com/go-acme/lego/v5/challenge/dns01"
)

const (
	protectedAliyunDomain       = "disabled-protected.invalid"
	aliyunZoneResolutionTimeout = 2 * time.Second
)

// AliyunZoneResolver 只读识别用户域名当前的权威 DNS 托管区。
// 它绝不能枚举 RAM 账号域名，也不能触发任何阿里云写操作。
type AliyunZoneResolver interface {
	Resolve(context.Context, string) (string, error)
}

type aliyunZoneResolverFunc func(context.Context, string) (string, error)

func (f aliyunZoneResolverFunc) Resolve(ctx context.Context, requested string) (string, error) {
	return f(ctx, requested)
}

type authoritativeAliyunZoneResolver struct {
	findZone func(context.Context, string) (string, error)
	timeout  time.Duration
}

func newDefaultAliyunZoneResolver() AliyunZoneResolver {
	return authoritativeAliyunZoneResolver{
		findZone: func(ctx context.Context, fqdn string) (string, error) {
			client := dns01.NewClient(&dns01.Options{Timeout: aliyunZoneResolutionTimeout})
			return client.FindZoneByFqdn(ctx, fqdn)
		},
		timeout: aliyunZoneResolutionTimeout,
	}
}

func (r authoritativeAliyunZoneResolver) Resolve(ctx context.Context, requested string) (string, error) {
	base, err := validateAliyunRequestedDomain(requested)
	if err != nil {
		return "", err
	}
	if r.findZone == nil {
		return "", errors.New("阿里云权威 DNS 托管区识别器未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = aliyunZoneResolutionTimeout
	}
	resolveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	zone, err := r.findZone(resolveCtx, base+".")
	if err != nil {
		return "", fmt.Errorf("无法识别域名 %s 的权威 DNS 托管区，已停止验证：%w", requested, err)
	}
	return normalizeAuthorizationDomain(zone), nil
}

// ResolveAliyunTargetDomain 根据公开 DNS 的 SOA 结果识别唯一权威托管区。
// SOA 查询可能读取父标签以定位权威区，但整个过程从不向候选父域写入 TXT 记录。
func ResolveAliyunTargetDomain(requested string) (string, error) {
	return resolveAliyunTargetDomain(context.Background(), requested, newDefaultAliyunZoneResolver())
}

func resolveAliyunTargetDomain(ctx context.Context, requested string, resolver AliyunZoneResolver) (string, error) {
	base, err := validateAliyunRequestedDomain(requested)
	if err != nil {
		return "", err
	}
	if resolver == nil {
		return "", errors.New("阿里云权威 DNS 托管区识别器未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	zone, err := resolver.Resolve(ctx, base)
	if err != nil {
		return "", err
	}
	zone = normalizeAuthorizationDomain(zone)
	if !domain.IsValidName(zone) || strings.HasPrefix(zone, "*.") {
		return "", fmt.Errorf("权威 DNS 托管区识别结果无效：%s", zone)
	}
	if isProtectedAliyunDomain(zone) {
		return "", fmt.Errorf("域名 %s 的权威 DNS 托管区 %s 受本机保护策略限制", requested, zone)
	}
	if base != zone && !strings.HasSuffix(base, "."+zone) {
		return "", fmt.Errorf("权威 DNS 托管区 %s 不属于待操作域名 %s，已停止验证", zone, requested)
	}
	return zone, nil
}

// validateAliyunRequestedDomain 仅做不访问网络的本地格式和保护策略检查。
// 这使受保护域在读取凭据、构造客户端或进行 SOA 查询前被拒绝。
func validateAliyunRequestedDomain(requested string) (string, error) {
	requested = normalizeAuthorizationDomain(requested)
	base := strings.TrimPrefix(requested, "*.")
	if !domain.IsValidName(requested) || base == "" {
		return "", errors.New("待操作域名格式无效")
	}
	if isProtectedAliyunDomain(base) {
		return "", fmt.Errorf("域名 %s 受本机保护策略限制，未构造阿里云客户端或调用云 API", requested)
	}
	return base, nil
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
		if _, err := validateAliyunRequestedDomain(name); err != nil {
			return err
		}
	}
	return nil
}
