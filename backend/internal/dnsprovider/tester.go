package dnsprovider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/challenge/dnspersist01"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type Factory interface {
	Create(providerID string, credentials map[string]string) (challenge.Provider, error)
}

type TXTResolver interface {
	LookupTXT(ctx context.Context, fqdn string) ([]string, error)
}

type Tester struct {
	factory             Factory
	resolver            TXTResolver
	aliyunAuthorization *AliyunAuthorizationChecker
	propagationTimeout  time.Duration
	propagationInterval time.Duration
}

const (
	// PermissionVerificationTimeout 是 DNS API 权限验证的整体请求上限。
	// 它与 ACME DNS-01 的公网传播等待相互独立。
	PermissionVerificationTimeout = 60 * time.Second

	// AliyunPermissionVerificationTimeout 限制阿里云账号内的创建、查询和清理闭环。
	// 该流程不等待公网 DNS 传播。
	AliyunPermissionVerificationTimeout = 6 * time.Second

	dnsCleanupTimeout                     = 30 * time.Second
	defaultDNSPermissionTXTLookupTimeout  = 30 * time.Second
	defaultDNSPermissionTXTLookupInterval = 2 * time.Second
)

func PermissionVerificationTimeoutFor(providerID string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(providerID), Aliyun) {
		return AliyunPermissionVerificationTimeout
	}
	return PermissionVerificationTimeout
}

func NewTester(factory Factory) *Tester {
	return NewTesterWithResolverAndSettings(factory, legoTXTResolver{}, defaultDNSPermissionTXTLookupTimeout, defaultDNSPermissionTXTLookupInterval)
}

func NewTesterWithResolver(factory Factory, resolver TXTResolver) *Tester {
	return NewTesterWithResolverAndSettings(factory, resolver, defaultDNSPermissionTXTLookupTimeout, defaultDNSPermissionTXTLookupInterval)
}

func NewTesterWithSettings(factory Factory, timeout, interval time.Duration) *Tester {
	return NewTesterWithResolverAndSettings(factory, legoTXTResolver{}, timeout, interval)
}

func NewTesterWithResolverAndSettings(factory Factory, resolver TXTResolver, timeout, interval time.Duration) *Tester {
	if timeout <= 0 {
		timeout = defaultDNSPermissionTXTLookupTimeout
	}
	if interval <= 0 || interval > timeout {
		interval = defaultDNSPermissionTXTLookupInterval
		if interval > timeout {
			interval = timeout
		}
	}
	return &Tester{
		factory:             factory,
		resolver:            resolver,
		aliyunAuthorization: NewAliyunAuthorizationChecker(),
		propagationTimeout:  timeout,
		propagationInterval: interval,
	}
}

type VerificationError struct {
	Report setup.DNSVerificationReport
	Err    error
}

func (e *VerificationError) Error() string {
	if e == nil || e.Err == nil {
		return "DNS API 在线验证失败"
	}
	return e.Err.Error()
}

func (e *VerificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (t *Tester) Verify(ctx context.Context, providerID, testDomain string, credentials map[string]string) (setup.DNSVerificationReport, error) {
	return t.VerifyWithReport(ctx, providerID, testDomain, credentials)
}

func (t *Tester) VerifyWithReport(ctx context.Context, providerID, testDomain string, credentials map[string]string) (setup.DNSVerificationReport, error) {
	report := setup.DNSVerificationReport{}
	if t == nil {
		return report, errors.New("DNS 验证服务未初始化")
	}
	testDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(testDomain), "."))
	if !domain.IsValidName(testDomain) {
		return report, fmt.Errorf("DNS 测试域名格式无效：%s", testDomain)
	}
	cleaned, err := ValidateCredentials(providerID, credentials)
	if err != nil {
		return report, err
	}
	if err := ValidateAliyunTargetDomains(providerID, []string{testDomain}); err != nil {
		return report, err
	}
	if strings.EqualFold(strings.TrimSpace(providerID), Aliyun) {
		return t.verifyAliyun(ctx, testDomain, cleaned)
	}
	if t.factory == nil || t.resolver == nil {
		return report, errors.New("DNS 验证服务未初始化")
	}
	provider, err := t.factory.Create(providerID, cleaned)
	if err != nil {
		return report, err
	}
	token, err := randomValue(24)
	if err != nil {
		return report, fmt.Errorf("生成 DNS 测试标识失败：%w", err)
	}
	keyAuth, err := randomValue(48)
	if err != nil {
		return report, fmt.Errorf("生成 DNS 测试授权值失败：%w", err)
	}
	challengeInfo := dns01.GetChallengeInfo(ctx, testDomain, keyAuth)
	report.RecordName = challengeInfo.EffectiveFQDN
	if err := provider.Present(ctx, testDomain, token, keyAuth); err != nil {
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "创建记录", Status: "failed", Message: "调用 DNS API 创建临时 TXT 记录失败。",
		})
		return report, &VerificationError{Report: report, Err: fmt.Errorf("创建 DNS 测试记录失败：%w", err)}
	}
	report.Stages = append(report.Stages, setup.DNSVerificationStage{
		Name: "创建记录", Status: "completed", Message: "已调用 DNS API 创建临时 TXT 记录。",
	})

	if err := waitForTXT(ctx, t.resolver, report.RecordName, challengeInfo.Value, t.propagationTimeout, t.propagationInterval); err != nil {
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "查询记录", Status: "failed", Message: "公网 DNS 查询未确认 TXT 记录已生效。",
		})
		cleanupErr := cleanupChallenge(ctx, provider, testDomain, token, keyAuth)
		if cleanupErr != nil {
			report.Stages = append(report.Stages, setup.DNSVerificationStage{
				Name: "清理记录", Status: "failed", Message: "测试记录清理失败，请检查 DNS API 权限。",
			})
			return report, &VerificationError{Report: report, Err: fmt.Errorf("DNS 测试记录未通过公网查询，且自动清理失败：%w", cleanupErr)}
		}
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "清理记录", Status: "completed", Message: "已清理未通过查询确认的临时 TXT 记录。",
		})
		return report, &VerificationError{Report: report, Err: fmt.Errorf("DNS 测试记录未通过公网查询：%w", err)}
	}
	report.Stages = append(report.Stages, setup.DNSVerificationStage{
		Name: "查询记录", Status: "completed", Message: "公网 DNS 查询已确认 TXT 记录生效。",
	})

	if err := cleanupChallenge(ctx, provider, testDomain, token, keyAuth); err != nil {
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "清理记录", Status: "failed", Message: "测试记录已创建并确认生效，但自动清理失败。",
		})
		return report, &VerificationError{Report: report, Err: fmt.Errorf("DNS 测试记录已创建并确认生效，但自动清理失败：%w", err)}
	}
	report.Stages = append(report.Stages, setup.DNSVerificationStage{
		Name: "清理记录", Status: "completed", Message: "已调用 DNS API 清理临时 TXT 记录。",
	})
	if values, err := t.resolver.LookupTXT(ctx, report.RecordName); err == nil && containsTXT(values, challengeInfo.Value) {
		report.Stages[len(report.Stages)-1] = setup.DNSVerificationStage{
			Name: "清理记录", Status: "warning", Message: "清理请求已成功，但公网 DNS 仍能查询到旧记录，可能正在等待缓存刷新。",
		}
	}
	return report, nil
}

func (t *Tester) verifyAliyun(ctx context.Context, testDomain string, credentials map[string]string) (setup.DNSVerificationReport, error) {
	report := setup.DNSVerificationReport{}
	if t.aliyunAuthorization == nil {
		return report, errors.New("阿里云 DNS 权限验证服务未初始化")
	}
	authorization, err := t.aliyunAuthorization.Verify(ctx, testDomain, credentials)
	report.RecordName = authorization.RecordName
	if len(authorization.Domains) == 0 || !authorization.Domains[0].Writable {
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "创建记录", Status: "failed", Message: "调用阿里云 DNS API 创建临时 TXT 记录失败。",
		})
		return report, &VerificationError{Report: report, Err: aliyunVerificationError(err)}
	}
	report.Stages = append(report.Stages, setup.DNSVerificationStage{
		Name: "创建记录", Status: "completed", Message: "已通过阿里云 DNS API 创建临时 TXT 记录。",
	})
	if !authorization.Domains[0].Readable {
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "查询记录", Status: "failed", Message: "阿里云 DNS API 未能精确查询并比对本次临时 TXT 记录。",
		})
		appendAliyunCleanupStage(&report, authorization.Domains[0].Cleanup)
		return report, &VerificationError{Report: report, Err: aliyunVerificationError(err)}
	}
	report.Stages = append(report.Stages, setup.DNSVerificationStage{
		Name: "查询记录", Status: "completed", Message: "已通过阿里云 DNS API 精确查询并比对临时 TXT 记录，未等待公网 DNS 传播。",
	})
	appendAliyunCleanupStage(&report, authorization.Domains[0].Cleanup)
	if err != nil || !authorization.Verified {
		return report, &VerificationError{Report: report, Err: aliyunVerificationError(err)}
	}
	return report, nil
}

func appendAliyunCleanupStage(report *setup.DNSVerificationReport, cleaned bool) {
	if cleaned {
		report.Stages = append(report.Stages, setup.DNSVerificationStage{
			Name: "清理记录", Status: "completed", Message: "已通过阿里云 DNS API 清理本次临时 TXT 记录。",
		})
		return
	}
	report.Stages = append(report.Stages, setup.DNSVerificationStage{
		Name: "清理记录", Status: "failed", Message: "临时 TXT 记录清理失败，不能确认阿里云 DNS 权限验证通过。",
	})
}

func aliyunVerificationError(err error) error {
	if err != nil {
		return err
	}
	return errors.New("阿里云 DNS 权限验证未通过")
}

func cleanupChallenge(ctx context.Context, provider challenge.Provider, testDomain, token, keyAuth string) error {
	cleanupBase := context.WithoutCancel(ctx)
	cleanupCtx, cancel := context.WithTimeout(cleanupBase, dnsCleanupTimeout)
	defer cancel()
	return provider.CleanUp(cleanupCtx, testDomain, token, keyAuth)
}

type legoTXTResolver struct{}

func (legoTXTResolver) LookupTXT(ctx context.Context, fqdn string) ([]string, error) {
	result, err := dnspersist01.DefaultClient().LookupTXT(ctx, fqdn)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(result.Records))
	for _, record := range result.Records {
		values = append(values, record.Value)
	}
	return values, nil
}

func waitForTXT(ctx context.Context, resolver TXTResolver, fqdn, expected string, timeout, interval time.Duration) error {
	if resolver == nil {
		return errors.New("TXT 查询服务未初始化")
	}
	if timeout <= 0 {
		timeout = defaultDNSPermissionTXTLookupTimeout
	}
	if interval <= 0 || interval > timeout {
		interval = defaultDNSPermissionTXTLookupInterval
		if interval > timeout {
			interval = timeout
		}
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		values, err := resolver.LookupTXT(ctx, fqdn)
		if err == nil && containsTXT(values, expected) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if err != nil {
				return fmt.Errorf("查询 TXT 记录失败：%w", err)
			}
			return errors.New("查询结果中未找到测试 TXT 值")
		case <-ticker.C:
		}
	}
}

func containsTXT(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func randomValue(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
