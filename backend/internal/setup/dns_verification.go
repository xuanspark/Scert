package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

const dnsVerificationLifetime = 15 * time.Minute

type DNSCredentialTester interface {
	Verify(ctx context.Context, providerID, testDomain string, credentials map[string]string) error
}

type SecretWriter interface {
	Put(ctx context.Context, reference string, value map[string]string) error
}

type DNSVerificationInput struct {
	ProviderID  string
	TestDomain  string
	Credentials map[string]string
}

type DNSVerificationResult struct {
	CredentialRef     string
	VerificationToken string
	ExpiresAt         time.Time
}

type dnsVerificationSession struct {
	ProviderID    string
	TestDomain    string
	CredentialRef string
	ExpiresAt     time.Time
}

type DNSVerificationManager struct {
	mu       sync.Mutex
	tester   DNSCredentialTester
	store    SecretWriter
	now      func() time.Time
	sessions map[string]dnsVerificationSession
}

func NewDNSVerificationManager(tester DNSCredentialTester, store SecretWriter) *DNSVerificationManager {
	return &DNSVerificationManager{
		tester: tester, store: store, now: time.Now,
		sessions: make(map[string]dnsVerificationSession),
	}
}

func (m *DNSVerificationManager) Verify(ctx context.Context, input DNSVerificationInput) (DNSVerificationResult, error) {
	if m == nil || m.tester == nil || m.store == nil {
		return DNSVerificationResult{}, errors.New("DNS 在线验证服务未初始化")
	}
	providerID := strings.ToLower(strings.TrimSpace(input.ProviderID))
	testDomain := normalizeTestDomain(input.TestDomain)
	if providerID == "" {
		return DNSVerificationResult{}, errors.New("DNS 服务商不能为空")
	}
	if !domain.IsValidName(testDomain) {
		return DNSVerificationResult{}, fmt.Errorf("DNS 测试域名格式无效：%s", testDomain)
	}
	if err := m.tester.Verify(ctx, providerID, testDomain, input.Credentials); err != nil {
		return DNSVerificationResult{}, err
	}
	credentialID, err := secureToken(18)
	if err != nil {
		return DNSVerificationResult{}, fmt.Errorf("生成 DNS 凭据引用失败：%w", err)
	}
	credentialRef := fmt.Sprintf("secret://dns/%s/%s", providerID, credentialID)
	if err := m.store.Put(ctx, credentialRef, input.Credentials); err != nil {
		return DNSVerificationResult{}, fmt.Errorf("加密保存 DNS 凭据失败：%w", err)
	}
	verificationToken, err := secureToken(32)
	if err != nil {
		return DNSVerificationResult{}, fmt.Errorf("生成 DNS 验证令牌失败：%w", err)
	}
	expiresAt := m.now().UTC().Add(dnsVerificationLifetime)
	m.mu.Lock()
	m.clearExpiredLocked(m.now().UTC())
	m.sessions[verificationToken] = dnsVerificationSession{
		ProviderID: providerID, TestDomain: testDomain,
		CredentialRef: credentialRef, ExpiresAt: expiresAt,
	}
	m.mu.Unlock()
	return DNSVerificationResult{
		CredentialRef: credentialRef, VerificationToken: verificationToken, ExpiresAt: expiresAt,
	}, nil
}

func (m *DNSVerificationManager) Check(token, providerID, credentialRef, testDomain string) error {
	return m.validateSession(token, providerID, credentialRef, testDomain, false)
}

func (m *DNSVerificationManager) Consume(token, providerID, credentialRef, testDomain string) error {
	return m.validateSession(token, providerID, credentialRef, testDomain, true)
}

func (m *DNSVerificationManager) validateSession(token, providerID, credentialRef, testDomain string, consume bool) error {
	if m == nil {
		return errors.New("DNS 在线验证服务未初始化")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("必须先完成 DNS API 在线验证")
	}
	now := m.now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearExpiredLocked(now)
	session, ok := m.sessions[token]
	if !ok {
		return errors.New("DNS 验证令牌无效或已经过期，请重新测试")
	}
	if session.ProviderID != strings.ToLower(strings.TrimSpace(providerID)) ||
		session.CredentialRef != strings.TrimSpace(credentialRef) ||
		session.TestDomain != normalizeTestDomain(testDomain) {
		return errors.New("DNS 验证结果与当前配置不匹配，请重新测试")
	}
	if consume {
		delete(m.sessions, token)
	}
	return nil
}

func (m *DNSVerificationManager) clearExpiredLocked(now time.Time) {
	for token, session := range m.sessions {
		if !session.ExpiresAt.After(now) {
			delete(m.sessions, token)
		}
	}
}

func normalizeTestDomain(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func secureToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
