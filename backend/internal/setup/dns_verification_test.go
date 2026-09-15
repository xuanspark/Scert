package setup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeDNSTester struct {
	err error
}

func TestDNSVerificationStoresOnlyCurrentAliyunCredentials(t *testing.T) {
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(fakeDNSTester{}, store)
	input := DNSVerificationInput{ProviderID: "aliyun", TestDomain: "www.example.com", Credentials: map[string]string{
		"access_key_id": "test-id", "access_key_secret": "test-secret",
		"managed_zones": "example.com", "allowed_domains": "www.example.com",
	}}
	first, err := manager.Verify(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.values[first.CredentialRef]["managed_zones"]; ok {
		t.Fatal("legacy managed zone must not be persisted as a credential")
	}
	if _, ok := store.values[first.CredentialRef]["allowed_domains"]; ok {
		t.Fatal("legacy allowed domain must not be persisted as a credential")
	}
	input.Credentials["access_key_secret"] = "new-secret"
	second, err := manager.Verify(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.CredentialRef == second.CredentialRef || store.values[first.CredentialRef]["access_key_secret"] != "test-secret" {
		t.Fatal("credential changes must not overwrite an already verified reference")
	}
	if err := manager.Check(first.VerificationToken, "aliyun", second.CredentialRef, input.TestDomain); err == nil {
		t.Fatal("old token must not authorize a new credential reference")
	}
}

func (f fakeDNSTester) Verify(context.Context, string, string, map[string]string) (DNSVerificationReport, error) {
	return DNSVerificationReport{}, f.err
}

type memorySecretWriter struct {
	mu        sync.Mutex
	values    map[string]map[string]string
	deleted   []string
	putErr    error
	deleteErr error
}

func (m *memorySecretWriter) Put(_ context.Context, reference string, value map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]map[string]string{}
	}
	copy := make(map[string]string, len(value))
	for key, item := range value {
		copy[key] = item
	}
	m.values[reference] = copy
	return m.putErr
}

func (m *memorySecretWriter) Delete(_ context.Context, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, reference)
	delete(m.values, reference)
	return m.deleteErr
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
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(fakeDNSTester{}, store)
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
	if len(store.deleted) != 1 || store.deleted[0] != result.CredentialRef {
		t.Fatalf("expired session did not delete temporary credential: %v", store.deleted)
	}
}

func TestDNSVerificationManagerDeletesCredentialWhenPutReportsFailure(t *testing.T) {
	store := &memorySecretWriter{putErr: errors.New("write failed")}
	manager := NewDNSVerificationManager(fakeDNSTester{}, store)
	_, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com",
		Credentials: map[string]string{"api_token": "secret"},
	})
	if err == nil {
		t.Fatal("Put failure unexpectedly succeeded")
	}
	if len(store.deleted) != 1 {
		t.Fatalf("Put failure did not trigger credential cleanup: %v", store.deleted)
	}
	if len(store.values) != 0 {
		t.Fatalf("Put failure left temporary credentials stored: %v", store.values)
	}
}

type countingDNSTester struct {
	mu    sync.Mutex
	calls int
	block <-chan struct{}
}

func (f *countingDNSTester) Verify(_ context.Context, _ string, _ string, _ map[string]string) (DNSVerificationReport, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	return DNSVerificationReport{}, nil
}

func (f *countingDNSTester) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestDNSVerificationManagerRejectsCapacityBeforeTestingOrStoring(t *testing.T) {
	tester := &countingDNSTester{}
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(tester, store)
	manager.sessions = make(map[string]dnsVerificationSession, dnsVerificationSessionCapacity)
	expiresAt := time.Now().UTC().Add(time.Hour)
	for index := 0; index < dnsVerificationSessionCapacity; index++ {
		manager.sessions[string(rune(index))] = dnsVerificationSession{CredentialRef: "secret://existing", ExpiresAt: expiresAt}
	}
	_, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com", Credentials: map[string]string{"api_token": "secret"},
	})
	if err == nil || tester.CallCount() != 0 || len(store.values) != 0 {
		t.Fatalf("capacity rejection must precede testing and storage: err=%v calls=%d values=%v", err, tester.CallCount(), store.values)
	}
}

func TestDNSVerificationManagerRejectsInFlightCapacityBeforeTestingOrStoring(t *testing.T) {
	tester := &countingDNSTester{}
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(tester, store)
	manager.inFlight = dnsVerificationInFlightCapacity
	_, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com", Credentials: map[string]string{"api_token": "secret"},
	})
	if err == nil || tester.CallCount() != 0 || len(store.values) != 0 {
		t.Fatalf("in-flight rejection must precede testing and storage: err=%v calls=%d values=%v", err, tester.CallCount(), store.values)
	}
}

func TestDNSVerificationManagerConsumesTokenOnlyOnceUnderConcurrency(t *testing.T) {
	manager := NewDNSVerificationManager(fakeDNSTester{}, &memorySecretWriter{})
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com", Credentials: map[string]string{"api_token": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- manager.Consume(result.VerificationToken, "cloudflare", result.CredentialRef, "example.com")
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent Consume successes = %d; want 1", successes)
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

type recordingDNSTester struct {
	credentials map[string]string
}

func (f *recordingDNSTester) Verify(_ context.Context, _ string, _ string, credentials map[string]string) (DNSVerificationReport, error) {
	f.credentials = credentials
	return DNSVerificationReport{}, nil
}

func TestDNSVerificationManagerNormalizesCredentialCopyBeforeTestingAndStoring(t *testing.T) {
	store := &memorySecretWriter{}
	tester := &recordingDNSTester{}
	manager := NewDNSVerificationManager(tester, store)
	inputCredentials := map[string]string{" api_token ": " secret-token "}
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "cloudflare", TestDomain: "example.com", Credentials: inputCredentials,
	})
	if err != nil {
		t.Fatalf("DNS 在线验证失败：%v", err)
	}
	if tester.credentials["api_token"] != "secret-token" {
		t.Fatalf("传给测试器的凭据未规范化：%v", tester.credentials)
	}
	if _, ok := tester.credentials[" api_token "]; ok {
		t.Fatal("测试器不应收到带首尾空格的凭据键")
	}
	stored := store.values[result.CredentialRef]
	if stored["api_token"] != "secret-token" {
		t.Fatalf("保存的凭据未规范化：%v", stored)
	}
	if inputCredentials[" api_token "] != " secret-token " {
		t.Fatal("规范化过程不应修改调用方的原始凭据 Map")
	}
}
