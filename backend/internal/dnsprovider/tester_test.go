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
	_, err := NewLegoFactory().Create(Aliyun, map[string]string{"access_key_id": "id", "access_key_secret": "secret"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("未开放适配器应返回指定错误：%v", err)
	}
}
