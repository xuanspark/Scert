package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsverification"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

const (
	dnsVerificationLifetime         = 15 * time.Minute
	dnsVerificationSessionCapacity  = 128
	dnsVerificationInFlightCapacity = 16
	dnsVerificationCleanupTimeout   = 30 * time.Second
)

type DNSCredentialTester interface {
	Verify(ctx context.Context, providerID, testDomain string, credentials map[string]string) (DNSVerificationReport, error)
}

type DNSVerificationStage = dnsverification.Stage

type DNSVerificationReport = dnsverification.Report

type DNSVerificationError struct {
	Report DNSVerificationReport
	Err    error
}

func (e *DNSVerificationError) Error() string {
	if e == nil || e.Err == nil {
		return "DNS API 在线验证失败"
	}
	return e.Err.Error()
}

func (e *DNSVerificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type SecretWriter interface {
	Put(ctx context.Context, reference string, value map[string]string) error
}

// SecretDeleter 用于回收过期、未绑定或保存失败的临时 DNS 凭据。
type SecretDeleter interface {
	Delete(ctx context.Context, reference string) error
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
	TestReport        DNSVerificationReport
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
	deleter  SecretDeleter
	now      func() time.Time
	sessions map[string]dnsVerificationSession
	inFlight int
}

func NewDNSVerificationManager(tester DNSCredentialTester, store SecretWriter) *DNSVerificationManager {
	deleter, _ := store.(SecretDeleter)
	return &DNSVerificationManager{
		tester: tester, store: store, deleter: deleter, now: time.Now,
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
	normalizedCredentials := normalizeCredentials(input.Credentials)
	cleanedCredentials, err := dnsprovider.ValidateCredentials(providerID, normalizedCredentials)
	if err != nil {
		return DNSVerificationResult{}, err
	}
	if err := m.reserveVerificationSlot(); err != nil {
		return DNSVerificationResult{}, err
	}
	released := false
	defer func() {
		if !released {
			m.releaseVerificationSlot()
		}
	}()
	testReport, err := m.tester.Verify(ctx, providerID, testDomain, cleanedCredentials)
	if err != nil {
		return DNSVerificationResult{TestReport: testReport}, &DNSVerificationError{
			Report: testReport,
			Err:    err,
		}
	}
	verificationToken, err := secureToken(32)
	if err != nil {
		return DNSVerificationResult{}, fmt.Errorf("生成 DNS 验证令牌失败：%w", err)
	}
	credentialID, err := secureToken(18)
	if err != nil {
		return DNSVerificationResult{}, fmt.Errorf("生成 DNS 凭据引用失败：%w", err)
	}
	credentialRef := fmt.Sprintf("secret://dns/%s/%s", providerID, credentialID)
	if err := m.store.Put(ctx, credentialRef, cleanedCredentials); err != nil {
		cleanupErr := m.deleteCredential(credentialRef)
		if cleanupErr != nil {
			m.logCleanupFailure()
			return DNSVerificationResult{}, errors.New("加密保存 DNS 凭据失败，临时凭据回收未确认")
		}
		return DNSVerificationResult{}, fmt.Errorf("加密保存 DNS 凭据失败：%w", err)
	}
	expiresAt := m.now().UTC().Add(dnsVerificationLifetime)
	m.mu.Lock()
	expiredRefs := m.clearExpiredLocked(m.now().UTC())
	m.sessions[verificationToken] = dnsVerificationSession{
		ProviderID: providerID, TestDomain: testDomain,
		CredentialRef: credentialRef, ExpiresAt: expiresAt,
	}
	m.inFlight--
	released = true
	m.mu.Unlock()
	m.deleteCredentials(expiredRefs)
	return DNSVerificationResult{
		CredentialRef: credentialRef, VerificationToken: verificationToken, ExpiresAt: expiresAt,
		TestReport: testReport,
	}, nil
}

func (m *DNSVerificationManager) Check(token, providerID, credentialRef, testDomain string) error {
	return m.validateSession(token, providerID, credentialRef, testDomain, false)
}

func (m *DNSVerificationManager) Consume(token, providerID, credentialRef, testDomain string) error {
	return m.validateSession(token, providerID, credentialRef, testDomain, true)
}

// Discard 按验证令牌精确回收尚未消费的 DNS 临时凭据，不影响其他初始化会话。
func (m *DNSVerificationManager) Discard(token string) error {
	if m == nil {
		return errors.New("DNS 在线验证服务未初始化")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("DNS 验证令牌不能为空")
	}
	now := m.now().UTC()
	m.mu.Lock()
	expiredRefs := m.clearExpiredLocked(now)
	session, ok := m.sessions[token]
	if ok {
		delete(m.sessions, token)
	}
	m.mu.Unlock()
	m.deleteCredentials(expiredRefs)
	if !ok {
		return errors.New("DNS 验证令牌无效或已经过期")
	}
	if err := m.deleteCredential(session.CredentialRef); err != nil {
		m.logCleanupFailure()
		return errors.New("DNS 临时凭据回收失败")
	}
	return nil
}

// DiscardAll 回收所有尚未消费的 DNS 验证令牌及其临时凭据。
func (m *DNSVerificationManager) DiscardAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	references := make([]string, 0, len(m.sessions))
	for token, session := range m.sessions {
		delete(m.sessions, token)
		if strings.TrimSpace(session.CredentialRef) != "" {
			references = append(references, session.CredentialRef)
		}
	}
	m.mu.Unlock()
	m.deleteCredentials(references)
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
	expiredRefs := m.clearExpiredLocked(now)
	session, ok := m.sessions[token]
	if !ok {
		m.mu.Unlock()
		m.deleteCredentials(expiredRefs)
		return errors.New("DNS 验证令牌无效或已经过期，请重新测试")
	}
	if session.ProviderID != strings.ToLower(strings.TrimSpace(providerID)) ||
		session.CredentialRef != strings.TrimSpace(credentialRef) ||
		session.TestDomain != normalizeTestDomain(testDomain) {
		m.mu.Unlock()
		m.deleteCredentials(expiredRefs)
		return errors.New("DNS 验证结果与当前配置不匹配，请重新测试")
	}
	if consume {
		delete(m.sessions, token)
	}
	m.mu.Unlock()
	m.deleteCredentials(expiredRefs)
	return nil
}

func (m *DNSVerificationManager) reserveVerificationSlot() error {
	if m == nil {
		return errors.New("DNS 在线验证服务未初始化")
	}
	now := m.now().UTC()
	m.mu.Lock()
	expiredRefs := m.clearExpiredLocked(now)
	if m.inFlight >= dnsVerificationInFlightCapacity {
		m.mu.Unlock()
		m.deleteCredentials(expiredRefs)
		return errors.New("DNS 在线验证请求过多，请稍后重试")
	}
	if len(m.sessions)+m.inFlight >= dnsVerificationSessionCapacity {
		m.mu.Unlock()
		m.deleteCredentials(expiredRefs)
		return errors.New("DNS 验证令牌数量已达上限，请等待现有验证完成或过期后重试")
	}
	m.inFlight++
	m.mu.Unlock()
	m.deleteCredentials(expiredRefs)
	return nil
}

func (m *DNSVerificationManager) releaseVerificationSlot() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.inFlight > 0 {
		m.inFlight--
	}
	m.mu.Unlock()
}

func (m *DNSVerificationManager) clearExpiredLocked(now time.Time) []string {
	var references []string
	for token, session := range m.sessions {
		if !session.ExpiresAt.After(now) {
			delete(m.sessions, token)
			references = append(references, session.CredentialRef)
		}
	}
	return references
}

func (m *DNSVerificationManager) deleteCredentials(references []string) {
	for _, reference := range references {
		if err := m.deleteCredential(reference); err != nil {
			m.logCleanupFailure()
		}
	}
}

func (m *DNSVerificationManager) logCleanupFailure() {
	log.Print("DNS 临时凭据回收失败")
}

func (m *DNSVerificationManager) deleteCredential(reference string) error {
	if m == nil || m.deleter == nil || strings.TrimSpace(reference) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), dnsVerificationCleanupTimeout)
	defer cancel()
	return m.deleter.Delete(ctx, reference)
}

func normalizeCredentials(credentials map[string]string) map[string]string {
	if credentials == nil {
		return nil
	}
	normalized := make(map[string]string, len(credentials))
	for key, value := range credentials {
		normalized[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return normalized
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
