package dnsprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"
)

type fakeFactory struct {
	provider challenge.Provider
	err      error
}

func (f fakeFactory) Create(string, map[string]string) (challenge.Provider, error) {
	return f.provider, f.err
}

type fakeProvider struct {
	presented          bool
	cleaned            bool
	cleanupSawCanceled bool
	presentErr         error
	cleanupErr         error
	domain             string
	keyAuth            string
}

type fakeTXTResolver struct {
	provider *fakeProvider
}

func (r fakeTXTResolver) LookupTXT(ctx context.Context, _ string) ([]string, error) {
	if r.provider == nil || r.provider.domain == "" || r.provider.keyAuth == "" {
		return nil, errors.New("测试 DNS 记录尚未创建")
	}
	return []string{dns01.GetChallengeInfo(ctx, r.provider.domain, r.provider.keyAuth).Value}, nil
}

func (p *fakeProvider) Present(_ context.Context, domain, _, keyAuth string) error {
	p.presented = true
	p.domain = domain
	p.keyAuth = keyAuth
	return p.presentErr
}

func (p *fakeProvider) CleanUp(ctx context.Context, _ string, _ string, _ string) error {
	p.cleaned = true
	p.cleanupSawCanceled = ctx.Err() != nil
	return p.cleanupErr
}

type missingTXTResolver struct{}

func (missingTXTResolver) LookupTXT(context.Context, string) ([]string, error) {
	return nil, nil
}

func TestTesterCreatesAndCleansChallengeRecord(t *testing.T) {
	provider := &fakeProvider{}
	tester := NewTesterWithResolver(fakeFactory{provider: provider}, fakeTXTResolver{provider: provider})
	report, err := tester.Verify(t.Context(), Cloudflare, "example.com", map[string]string{"api_token": "secret-token"})
	if err != nil {
		t.Fatalf("DNS 测试失败：%v", err)
	}
	if !provider.presented || !provider.cleaned {
		t.Fatalf("DNS 测试未完整执行：presented=%v cleaned=%v", provider.presented, provider.cleaned)
	}
	if report.RecordName == "" || len(report.Stages) != 3 {
		t.Fatalf("DNS 测试阶段报告不完整：%+v", report)
	}
	for index, stage := range report.Stages {
		if stage.Status != "completed" && !(index == 2 && stage.Status == "warning") {
			t.Fatalf("DNS 测试阶段状态异常：%+v", report.Stages)
		}
	}
}

func TestTesterDoesNotCleanupWhenPresentFails(t *testing.T) {
	provider := &fakeProvider{presentErr: errors.New("拒绝访问")}
	tester := NewTester(fakeFactory{provider: provider})
	_, err := tester.Verify(t.Context(), Cloudflare, "example.com", map[string]string{"api_token": "secret-token"})
	if err == nil {
		t.Fatal("创建测试记录失败时应返回错误")
	}
	if provider.cleaned {
		t.Fatal("创建测试记录失败时不应执行清理")
	}
}

func TestTesterCleansUpWithIndependentContextAfterQueryCancellation(t *testing.T) {
	provider := &fakeProvider{}
	tester := NewTesterWithResolver(fakeFactory{provider: provider}, missingTXTResolver{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tester.Verify(ctx, Cloudflare, "example.com", map[string]string{"api_token": "secret-token"})
	if err == nil {
		t.Fatal("公网查询被取消时应返回错误")
	}
	if !provider.cleaned {
		t.Fatal("公网查询被取消时仍应尝试清理测试记录")
	}
	if provider.cleanupSawCanceled {
		t.Fatal("清理测试记录不应继承已取消的请求上下文")
	}
}

func TestTesterReportsCleanupFailureAfterQueryFailure(t *testing.T) {
	provider := &fakeProvider{cleanupErr: errors.New("权限不足")}
	tester := NewTesterWithResolver(fakeFactory{provider: provider}, missingTXTResolver{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := tester.Verify(ctx, Cloudflare, "example.com", map[string]string{"api_token": "secret-token"})
	if err == nil || !strings.Contains(err.Error(), "自动清理失败") {
		t.Fatalf("查询失败且清理失败时应返回组合诊断：%v", err)
	}
	if len(report.Stages) != 3 || report.Stages[1].Status != "failed" || report.Stages[2].Status != "failed" {
		t.Fatalf("查询和清理失败阶段报告不完整：%+v", report.Stages)
	}
}

func TestValidateCredentialsRejectsUnknownField(t *testing.T) {
	_, err := ValidateCredentials(Cloudflare, map[string]string{"api_token": "token", "unexpected": "value"})
	if err == nil {
		t.Fatal("未知凭据字段应被拒绝")
	}
}

func TestCNAMEProviderIsAvailable(t *testing.T) {
	descriptor, err := GetDescriptor(CNAME)
	if err != nil {
		t.Fatalf("failed to read CNAME descriptor: %v", err)
	}
	if !descriptor.Available {
		t.Fatal("CNAME delegation adapter should be available")
	}
}

func TestLegoFactoryCreatesConfiguredProviders(t *testing.T) {
	tests := []struct {
		name        string
		providerID  string
		credentials map[string]string
	}{
		{
			name:       "阿里云 DNS",
			providerID: Aliyun,
			credentials: map[string]string{
				"access_key_id":     "id",
				"access_key_secret": "secret",
			},
		},
		{
			name:       "DNSPod",
			providerID: DNSPod,
			credentials: map[string]string{
				"secret_id":  "id",
				"secret_key": "secret",
			},
		},
		{
			name:       "DNSPod",
			providerID: HuaweiCloud,
			credentials: map[string]string{
				"access_key_id":     "id",
				"secret_access_key": "secret",
				"region":            "invalid-region",
			},
		},
		{
			name:       "华为云无效区域校验",
			providerID: TencentCloud,
			credentials: map[string]string{
				"secret_id":  "id",
				"secret_key": "secret",
			},
		},
		{
			name:       "AWS Route 53",
			providerID: Route53,
			credentials: map[string]string{
				"access_key_id":     "id",
				"secret_access_key": "secret",
				"region":            "us-east-1",
			},
		},
	}

	factory := NewLegoFactory()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := factory.Create(test.providerID, test.credentials)
			if test.providerID == HuaweiCloud {
				if err == nil {
					t.Fatal("无效的华为云区域应被拒绝")
				}
				if errors.Is(err, ErrProviderUnavailable) {
					t.Fatalf("华为云已接入，不应返回未开放错误：%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("创建 Provider 失败：%v", err)
			}
			if provider == nil {
				t.Fatal("创建 Provider 后返回空实例")
			}
		})
	}
}

func TestGoogleCloudFactoryRejectsMalformedCredentials(t *testing.T) {
	_, err := NewLegoFactory().Create(GoogleCloud, map[string]string{
		"service_account_json": "{}",
		"project":              "test-project",
	})
	if err == nil {
		t.Fatal("格式错误的 Google Cloud 服务账号凭据应被拒绝")
	}
	if errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Google Cloud 已接入，不应返回未开放错误：%v", err)
	}
}
