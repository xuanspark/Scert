package setup

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeDNSTester struct {
	err error
}

func (f fakeDNSTester) Verify(context.Context, string, string, map[string]string) error {
	return f.err
}

type memorySecretWriter struct {
	values map[string]map[string]string
}

func (m *memorySecretWriter) Put(_ context.Context, reference string, value map[string]string) error {
	if m.values == nil {
		m.values = map[string]map[string]string{}
	}
	m.values[reference] = value
	return nil
}

func TestDNSVerificationManagerIssuesOneTimeToken(t *testing.T) {
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(fakeDNSTester{}, store)
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com",
		Credentials: map[string]string{"api_token": "secret"},
	})
	if err != nil {
		t.Fatalf("DNS 在线验证失败：%v", err)
	}
	if result.CredentialRef == "" || result.VerificationToken == "" {
		t.Fatalf("DNS 验证结果不完整：%+v", result)
	}
	if len(store.values) != 1 {
		t.Fatalf("DNS 凭据没有写入敏感信息存储：%v", store.values)
	}
	if err := manager.Check(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com"); err != nil {
		t.Fatalf("检查 DNS 验证令牌失败：%v", err)
	}
	if err := manager.Check(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com"); err != nil {
		t.Fatalf("检查 DNS 验证令牌不应消费令牌：%v", err)
	}
	if err := manager.Consume(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com"); err != nil {
		t.Fatalf("消费 DNS 验证令牌失败：%v", err)
	}
	if err := manager.Check(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com"); err == nil {
		t.Fatal("已消费的 DNS 验证令牌不应继续通过检查")
	}
	if err := manager.Consume(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com"); err == nil {
		t.Fatal("DNS 验证令牌只能使用一次")
	}
}

func TestDNSVerificationManagerRejectsMismatchedConfiguration(t *testing.T) {
	manager := NewDNSVerificationManager(fakeDNSTester{}, &memorySecretWriter{})
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com",
		Credentials: map[string]string{"api_token": "secret"},
	})
	if err != nil {
		t.Fatalf("DNS 在线验证失败：%v", err)
	}
	if err := manager.Consume(result.VerificationToken, "cloudflare", result.CredentialRef, "other.example.com"); err == nil {
		t.Fatal("DNS 验证结果不应允许绑定到其他域名")
	}
}

func TestDNSVerificationManagerRejectsExpiredToken(t *testing.T) {
	manager := NewDNSVerificationManager(fakeDNSTester{}, &memorySecretWriter{})
	now := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com",
		Credentials: map[string]string{"api_token": "secret"},
	})
	if err != nil {
		t.Fatalf("DNS 在线验证失败：%v", err)
	}
	now = now.Add(dnsVerificationLifetime + time.Second)
	if err := manager.Consume(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com"); err == nil {
		t.Fatal("过期 DNS 验证令牌应被拒绝")
	}
}

func TestDNSVerificationManagerDoesNotStoreFailedCredential(t *testing.T) {
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(fakeDNSTester{err: errors.New("权限不足")}, store)
	_, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com",
		Credentials: map[string]string{"api_token": "secret"},
	})
	if err == nil {
		t.Fatal("DNS 测试失败时应返回错误")
	}
	if len(store.values) != 0 {
		t.Fatal("DNS 测试失败时不应保存凭据")
	}
}
