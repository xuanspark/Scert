package dnsprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/go-acme/lego/v5/challenge"
)

type fakeFactory struct {
	provider challenge.Provider
	err      error
}

func (f fakeFactory) Create(string, map[string]string) (challenge.Provider, error) {
	return f.provider, f.err
}

type fakeProvider struct {
	presented  bool
	cleaned    bool
	presentErr error
	cleanupErr error
}

func (p *fakeProvider) Present(context.Context, string, string, string) error {
	p.presented = true
	return p.presentErr
}

func (p *fakeProvider) CleanUp(context.Context, string, string, string) error {
	p.cleaned = true
	return p.cleanupErr
}

func TestTesterCreatesAndCleansChallengeRecord(t *testing.T) {
	provider := &fakeProvider{}
	tester := NewTester(fakeFactory{provider: provider})
	err := tester.Verify(t.Context(), Cloudflare, "example.com", map[string]string{"api_token": "secret-token"})
	if err != nil {
		t.Fatalf("DNS 测试失败：%v", err)
	}
	if !provider.presented || !provider.cleaned {
		t.Fatalf("DNS 测试未完整执行：presented=%v cleaned=%v", provider.presented, provider.cleaned)
	}
}

func TestTesterDoesNotCleanupWhenPresentFails(t *testing.T) {
	provider := &fakeProvider{presentErr: errors.New("拒绝访问")}
	tester := NewTester(fakeFactory{provider: provider})
	err := tester.Verify(t.Context(), Cloudflare, "example.com", map[string]string{"api_token": "secret-token"})
	if err == nil {
		t.Fatal("创建测试记录失败时应返回错误")
	}
	if provider.cleaned {
		t.Fatal("创建测试记录失败时不应执行清理")
	}
}

func TestValidateCredentialsRejectsUnknownField(t *testing.T) {
	_, err := ValidateCredentials(Cloudflare, map[string]string{"api_token": "token", "unexpected": "value"})
	if err == nil {
		t.Fatal("未知凭据字段应被拒绝")
	}
}

func TestUnavailableProviderReturnsTypedError(t *testing.T) {
	_, err := NewLegoFactory().Create(CNAME, map[string]string{"delegated_domain": "acme.example.com", "delegated_provider": "cloudflare"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("未开放适配器应返回指定错误：%v", err)
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
			name:       "华为云无效区域校验",
			providerID: HuaweiCloud,
			credentials: map[string]string{
				"access_key_id":     "id",
				"secret_access_key": "secret",
				"region":            "invalid-region",
			},
		},
		{
			name:       "腾讯云",
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
