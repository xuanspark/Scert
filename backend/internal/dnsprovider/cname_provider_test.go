package dnsprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/go-acme/lego/v5/challenge"
)

type delegationRecordingProvider struct {
	presentDomain string
	cleanupDomain string
	presentToken  string
	cleanupToken  string
	presentErr    error
	cleanupErr    error
}

func (p *delegationRecordingProvider) Present(_ context.Context, domain, token, _ string) error {
	p.presentDomain = domain
	p.presentToken = token
	return p.presentErr
}

func (p *delegationRecordingProvider) CleanUp(_ context.Context, domain, token, _ string) error {
	p.cleanupDomain = domain
	p.cleanupToken = token
	return p.cleanupErr
}

var _ challenge.Provider = (*delegationRecordingProvider)(nil)

func TestCNAMEDelegationProviderForwardsChallengeDomain(t *testing.T) {
	target := &delegationRecordingProvider{}
	provider, err := newCNAMEDelegationProvider(" ACME.Example.com. ", target)
	if err != nil {
		t.Fatalf("创建 CNAME 委派 Provider 失败：%v", err)
	}
	ctx := context.Background()
	if err := provider.Present(ctx, "www.example.net", "token", "key-auth"); err != nil {
		t.Fatalf("转发创建 TXT 记录失败：%v", err)
	}
	if target.presentDomain != "acme.example.com" || target.presentToken != "token" {
		t.Fatalf("创建记录未转发到规范化委派域：domain=%q token=%q", target.presentDomain, target.presentToken)
	}
	if err := provider.CleanUp(ctx, "www.example.net", "token", "key-auth"); err != nil {
		t.Fatalf("转发清理 TXT 记录失败：%v", err)
	}
	if target.cleanupDomain != "acme.example.com" || target.cleanupToken != "token" {
		t.Fatalf("清理记录未转发到规范化委派域：domain=%q token=%q", target.cleanupDomain, target.cleanupToken)
	}
}

func TestCNAMEDelegationProviderPropagatesTargetErrors(t *testing.T) {
	presentErr := errors.New("创建失败")
	cleanupErr := errors.New("清理失败")
	target := &delegationRecordingProvider{presentErr: presentErr, cleanupErr: cleanupErr}
	provider, err := newCNAMEDelegationProvider("acme.example.com", target)
	if err != nil {
		t.Fatalf("创建 CNAME 委派 Provider 失败：%v", err)
	}
	if err := provider.Present(context.Background(), "example.com", "token", "key-auth"); !errors.Is(err, presentErr) {
		t.Fatalf("创建错误未透传：%v", err)
	}
	if err := provider.CleanUp(context.Background(), "example.com", "token", "key-auth"); !errors.Is(err, cleanupErr) {
		t.Fatalf("清理错误未透传：%v", err)
	}
}

func TestLegoFactoryCreatesCNAMEDelegationProvider(t *testing.T) {
	provider, err := NewLegoFactory().Create(CNAME, map[string]string{
		"delegated_domain":           "acme.example.com",
		"delegated_provider":         Cloudflare,
		"delegated_credentials_json": `{"api_token":"test-token"}`,
	})
	if err != nil {
		t.Fatalf("创建 CNAME 委派 Provider 失败：%v", err)
	}
	if provider == nil {
		t.Fatal("CNAME 委派 Provider 不应为空")
	}
}

func TestLegoFactoryRejectsRecursiveCNAMEDelegation(t *testing.T) {
	_, err := NewLegoFactory().Create(CNAME, map[string]string{
		"delegated_domain":           "acme.example.com",
		"delegated_provider":         CNAME,
		"delegated_credentials_json": `{}`,
	})
	if !errors.Is(err, ErrDelegatedProviderUnavailable) {
		t.Fatalf("递归 CNAME 委派应被拒绝：%v", err)
	}
}

func TestLegoFactoryRejectsInvalidCNAMEDelegationCredentials(t *testing.T) {
	_, err := NewLegoFactory().Create(CNAME, map[string]string{
		"delegated_domain":           "acme.example.com",
		"delegated_provider":         Cloudflare,
		"delegated_credentials_json": `[]`,
	})
	if !errors.Is(err, ErrInvalidDelegatedCredentials) {
		t.Fatalf("非对象 CNAME 凭据应被拒绝：%v", err)
	}
}
