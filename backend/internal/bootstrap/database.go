package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

const (
	databaseVerificationLifetime         = 15 * time.Minute
	databaseVerificationCapacity         = 128
	databaseVerificationInFlightCapacity = 8
	databaseVerificationCleanupTimeout   = 10 * time.Second
)

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type DatabaseSecretStore interface {
	database.CredentialReader
	Put(context.Context, string, map[string]string) error
	Delete(context.Context, string) error
}

type DatabaseInput struct {
	Driver          string `json:"driver"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Database        string `json:"database"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	TLSMode         string `json:"tls_mode"`
	RootCertificate string `json:"root_certificate"`
}

type DatabaseVerificationResult struct {
	Config            config.DatabaseConfig `json:"database"`
	CredentialRef     string                `json:"credential_ref,omitempty"`
	VerificationToken string                `json:"verification_token"`
	ExpiresAt         time.Time             `json:"expires_at"`
}

type databaseVerificationSession struct {
	Config        config.DatabaseConfig
	CredentialRef string
	ExpiresAt     time.Time
}

type expiredDatabaseVerificationSession struct {
	Token   string
	Session databaseVerificationSession
}

// DatabaseVerificationSession 仅供服务端完整初始化事务读取验证结果。
// 所有字段均禁止 JSON 序列化，避免凭据引用被误写入 HTTP 响应。
type DatabaseVerificationSession struct {
	Config        config.DatabaseConfig `json:"-"`
	CredentialRef string                `json:"-"`
	ExpiresAt     time.Time             `json:"-"`
}

type DatabaseVerificationManager struct {
	mu             sync.Mutex
	base           config.Config
	store          DatabaseSecretStore
	verify         func(context.Context, config.DatabaseConfig) error
	now            func() time.Time
	sessions       map[string]databaseVerificationSession
	expiredSession map[string]expiredDatabaseVerificationSession
	pendingCleanup map[string]struct{}
	inFlight       int
}

func NewDatabaseVerificationManager(base config.Config, store DatabaseSecretStore) *DatabaseVerificationManager {
	return &DatabaseVerificationManager{
		base: base, store: store, verify: database.VerifyWritable, now: time.Now,
		sessions:       make(map[string]databaseVerificationSession),
		expiredSession: make(map[string]expiredDatabaseVerificationSession),
		pendingCleanup: make(map[string]struct{}),
	}
}

func (m *DatabaseVerificationManager) Verify(ctx context.Context, input DatabaseInput) (DatabaseVerificationResult, error) {
	if m == nil || m.verify == nil || m.store == nil {
		return DatabaseVerificationResult{}, errors.New("数据库验证服务尚未初始化")
	}
	m.retryPendingCredentialCleanup()
	if err := m.reserve(); err != nil {
		return DatabaseVerificationResult{}, err
	}
	released := false
	defer func() {
		if !released {
			m.release()
		}
	}()

	persisted, credentials, err := buildDatabaseConfig(m.base, input)
	if err != nil {
		return DatabaseVerificationResult{}, err
	}
	credentialRef := ""
	keepCredential := false
	defer func() {
		if !keepCredential && credentialRef != "" {
			_ = m.deleteOrQueueCredential(credentialRef)
		}
	}()
	if len(credentials) > 0 {
		credentialRef, err = database.NewCredentialReference()
		if err != nil {
			return DatabaseVerificationResult{}, err
		}
		persisted.CredentialRef = credentialRef
		if err := m.store.Put(ctx, credentialRef, credentials); err != nil {
			return DatabaseVerificationResult{}, fmt.Errorf("加密保存数据库凭据失败：%w", err)
		}
	}
	resolved, err := database.ResolveConfig(ctx, persisted, m.store)
	if err != nil {
		return DatabaseVerificationResult{}, err
	}
	if err := m.verify(ctx, resolved); err != nil {
		return DatabaseVerificationResult{}, fmt.Errorf("数据库连接或写权限验证失败：%w", err)
	}
	token, err := randomToken(32)
	if err != nil {
		return DatabaseVerificationResult{}, fmt.Errorf("生成数据库验证令牌失败：%w", err)
	}
	expiresAt := m.now().UTC().Add(databaseVerificationLifetime)
	m.mu.Lock()
	expired := m.clearExpiredLocked(m.now().UTC())
	m.sessions[token] = databaseVerificationSession{
		Config: persisted, CredentialRef: credentialRef, ExpiresAt: expiresAt,
	}
	m.inFlight--
	released = true
	m.mu.Unlock()
	m.deleteExpiredSessions(expired)
	keepCredential = true
	return DatabaseVerificationResult{
		Config: persisted, CredentialRef: credentialRef,
		VerificationToken: token, ExpiresAt: expiresAt,
	}, nil
}

func (m *DatabaseVerificationManager) Check(token, credentialRef string) (config.DatabaseConfig, error) {
	return m.session(token, credentialRef, false)
}

func (m *DatabaseVerificationManager) Consume(token, credentialRef string) (config.DatabaseConfig, error) {
	return m.session(token, credentialRef, true)
}

// Resolve 按验证令牌读取完整初始化所需的服务端状态，但不消费令牌。
func (m *DatabaseVerificationManager) Resolve(token string) (DatabaseVerificationSession, error) {
	session, err := m.resolveSession(token, false)
	if err != nil {
		return DatabaseVerificationSession{}, err
	}
	return DatabaseVerificationSession{
		Config:        session.Config,
		CredentialRef: session.CredentialRef,
		ExpiresAt:     session.ExpiresAt,
	}, nil
}

// Claim 原子领取验证结果，并把凭据回收责任移交给初始化事务。
// 领取后令牌立即失效，不再受过期或批量清理影响；提交失败时必须调用返回的回收函数。
func (m *DatabaseVerificationManager) Claim(token string) (DatabaseVerificationSession, func() error, error) {
	session, err := m.resolveSession(token, true)
	if err != nil {
		return DatabaseVerificationSession{}, nil, err
	}
	cleanup := sync.OnceValue(func() error { return m.deleteOrQueueCredential(session.CredentialRef) })
	return DatabaseVerificationSession{
		Config: session.Config, CredentialRef: session.CredentialRef, ExpiresAt: session.ExpiresAt,
	}, cleanup, nil
}

// Discard 按验证令牌精确回收尚未消费的数据库凭据，不影响其他初始化会话。
func (m *DatabaseVerificationManager) Discard(token string) error {
	if m == nil {
		return errors.New("数据库验证服务尚未初始化")
	}
	m.retryPendingCredentialCleanup()
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("数据库验证令牌不能为空")
	}
	now := m.now().UTC()
	m.mu.Lock()
	expired := m.clearExpiredLocked(now)
	session, ok := m.sessions[token]
	if ok {
		delete(m.sessions, token)
	}
	m.mu.Unlock()
	m.deleteExpiredSessions(expired)
	if !ok {
		return errors.New("数据库验证令牌无效或已经过期")
	}
	if err := m.deleteCredential(session.CredentialRef); err != nil {
		m.restoreSession(token, session)
		return errors.New("数据库临时凭据回收失败")
	}
	return nil
}

func (m *DatabaseVerificationManager) session(token, credentialRef string, consume bool) (config.DatabaseConfig, error) {
	if m == nil {
		return config.DatabaseConfig{}, errors.New("数据库验证服务尚未初始化")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return config.DatabaseConfig{}, errors.New("必须先完成数据库连接和写权限测试")
	}
	m.retryPendingCredentialCleanup()
	now := m.now().UTC()
	m.mu.Lock()
	expired := m.clearExpiredLocked(now)
	session, ok := m.sessions[token]
	if !ok {
		m.mu.Unlock()
		m.deleteExpiredSessions(expired)
		return config.DatabaseConfig{}, errors.New("数据库验证令牌无效或已经过期，请重新测试")
	}
	if session.CredentialRef != strings.TrimSpace(credentialRef) {
		m.mu.Unlock()
		m.deleteExpiredSessions(expired)
		return config.DatabaseConfig{}, errors.New("数据库验证结果与当前配置不匹配，请重新测试")
	}
	if consume {
		delete(m.sessions, token)
	}
	m.mu.Unlock()
	m.deleteExpiredSessions(expired)
	return session.Config, nil
}

func (m *DatabaseVerificationManager) resolveSession(token string, claim bool) (databaseVerificationSession, error) {
	if m == nil {
		return databaseVerificationSession{}, errors.New("数据库验证服务尚未初始化")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return databaseVerificationSession{}, errors.New("必须先完成数据库连接和写权限测试")
	}
	m.retryPendingCredentialCleanup()
	now := m.now().UTC()
	m.mu.Lock()
	expired := m.clearExpiredLocked(now)
	session, ok := m.sessions[token]
	if ok && claim {
		delete(m.sessions, token)
	}
	m.mu.Unlock()
	m.deleteExpiredSessions(expired)
	if !ok {
		return databaseVerificationSession{}, errors.New("数据库验证令牌无效或已经过期，请重新测试")
	}
	return session, nil
}

// DiscardAll 回收所有尚未消费的数据库验证令牌及其临时凭据。
func (m *DatabaseVerificationManager) DiscardAll() {
	m.DiscardAllExcept()
}

// DiscardAllExcept 回收全部尚未消费的数据库验证令牌，但保留已经成为正式配置依赖的凭据。
func (m *DatabaseVerificationManager) DiscardAllExcept(protectedReferences ...string) {
	if m == nil {
		return
	}
	m.retryPendingCredentialCleanup()
	protected := protectedCredentialReferences(protectedReferences)
	now := m.now().UTC()
	m.mu.Lock()
	expired := m.clearExpiredLocked(now)
	discarded := make([]expiredDatabaseVerificationSession, 0, len(m.sessions))
	for token, session := range m.sessions {
		delete(m.sessions, token)
		reference := strings.TrimSpace(session.CredentialRef)
		if reference != "" && !protected[reference] {
			discarded = append(discarded, expiredDatabaseVerificationSession{Token: token, Session: session})
		}
	}
	for token, entry := range m.expiredSession {
		reference := strings.TrimSpace(entry.Session.CredentialRef)
		if protected[reference] {
			delete(m.expiredSession, token)
			continue
		}
		alreadyQueued := false
		for _, item := range expired {
			if item.Token == token {
				alreadyQueued = true
				break
			}
		}
		if !alreadyQueued {
			expired = append(expired, entry)
		}
	}
	m.mu.Unlock()
	m.deleteExpiredSessions(expired)
	m.deleteDiscardedSessions(discarded)
}

func protectedCredentialReferences(references []string) map[string]bool {
	protected := make(map[string]bool, len(references))
	for _, reference := range references {
		if reference = strings.TrimSpace(reference); reference != "" {
			protected[reference] = true
		}
	}
	return protected
}

func (m *DatabaseVerificationManager) reserve() error {
	now := m.now().UTC()
	m.mu.Lock()
	expired := m.clearExpiredLocked(now)
	if m.inFlight >= databaseVerificationInFlightCapacity {
		m.mu.Unlock()
		m.deleteExpiredSessions(expired)
		return errors.New("数据库验证请求过多，请稍后重试")
	}
	if len(m.sessions)+len(m.expiredSession)+m.inFlight >= databaseVerificationCapacity {
		m.mu.Unlock()
		m.deleteExpiredSessions(expired)
		return errors.New("数据库验证令牌数量已达上限，请等待现有验证完成或过期后重试")
	}
	m.inFlight++
	m.mu.Unlock()
	m.deleteExpiredSessions(expired)
	return nil
}

func (m *DatabaseVerificationManager) release() {
	m.mu.Lock()
	if m.inFlight > 0 {
		m.inFlight--
	}
	m.mu.Unlock()
}

func (m *DatabaseVerificationManager) clearExpiredLocked(now time.Time) []expiredDatabaseVerificationSession {
	expired := make([]expiredDatabaseVerificationSession, 0)
	for token, session := range m.sessions {
		if session.ExpiresAt.After(now) {
			continue
		}
		entry := expiredDatabaseVerificationSession{Token: token, Session: session}
		delete(m.sessions, token)
		m.expiredSession[token] = entry
		expired = append(expired, entry)
	}
	return expired
}

func (m *DatabaseVerificationManager) deleteExpiredSessions(entries []expiredDatabaseVerificationSession) {
	for _, entry := range entries {
		reference := strings.TrimSpace(entry.Session.CredentialRef)
		if err := m.deleteCredential(reference); err != nil {
			m.restoreExpiredSession(entry)
			continue
		}
		m.removeExpiredSession(entry)
		m.forgetPendingCleanup(reference)
	}
}

func (m *DatabaseVerificationManager) deleteDiscardedSessions(entries []expiredDatabaseVerificationSession) {
	for _, entry := range entries {
		reference := strings.TrimSpace(entry.Session.CredentialRef)
		if err := m.deleteCredential(reference); err != nil {
			m.restoreSession(entry.Token, entry.Session)
			continue
		}
		m.forgetPendingCleanup(reference)
	}
}

func (m *DatabaseVerificationManager) deleteOrQueueCredential(reference string) error {
	if err := m.deleteCredential(reference); err != nil {
		m.rememberPendingCleanup(reference)
		return err
	}
	m.forgetPendingCleanup(reference)
	return nil
}

func (m *DatabaseVerificationManager) retryPendingCredentialCleanup() {
	if m == nil {
		return
	}
	m.mu.Lock()
	references := make([]string, 0, len(m.pendingCleanup))
	for reference := range m.pendingCleanup {
		references = append(references, reference)
	}
	m.mu.Unlock()
	for _, reference := range references {
		if err := m.deleteCredential(reference); err == nil {
			m.forgetPendingCleanup(reference)
		}
	}
}

func (m *DatabaseVerificationManager) rememberPendingCleanup(reference string) {
	reference = strings.TrimSpace(reference)
	if reference == "" || m == nil {
		return
	}
	m.mu.Lock()
	m.pendingCleanup[reference] = struct{}{}
	m.mu.Unlock()
}

func (m *DatabaseVerificationManager) forgetPendingCleanup(reference string) {
	reference = strings.TrimSpace(reference)
	if reference == "" || m == nil {
		return
	}
	m.mu.Lock()
	delete(m.pendingCleanup, reference)
	m.mu.Unlock()
}

func (m *DatabaseVerificationManager) restoreSession(token string, session databaseVerificationSession) {
	if m == nil || strings.TrimSpace(token) == "" {
		return
	}
	m.mu.Lock()
	if _, exists := m.sessions[token]; !exists {
		if _, expired := m.expiredSession[token]; !expired {
			m.sessions[token] = session
		}
	}
	m.mu.Unlock()
}

func (m *DatabaseVerificationManager) restoreExpiredSession(entry expiredDatabaseVerificationSession) {
	if m == nil || strings.TrimSpace(entry.Token) == "" {
		return
	}
	m.mu.Lock()
	if current, exists := m.expiredSession[entry.Token]; exists && current.Session.CredentialRef == entry.Session.CredentialRef {
		delete(m.expiredSession, entry.Token)
		if _, active := m.sessions[entry.Token]; !active {
			m.sessions[entry.Token] = entry.Session
		}
	}
	m.mu.Unlock()
}

func (m *DatabaseVerificationManager) removeExpiredSession(entry expiredDatabaseVerificationSession) {
	if m == nil || strings.TrimSpace(entry.Token) == "" {
		return
	}
	m.mu.Lock()
	if current, exists := m.expiredSession[entry.Token]; exists && current.Session.CredentialRef == entry.Session.CredentialRef {
		delete(m.expiredSession, entry.Token)
	}
	m.mu.Unlock()
}

func (m *DatabaseVerificationManager) deleteCredential(reference string) error {
	if strings.TrimSpace(reference) == "" || m == nil || m.store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), databaseVerificationCleanupTimeout)
	defer cancel()
	return m.store.Delete(ctx, reference)
}

func buildDatabaseConfig(base config.Config, input DatabaseInput) (config.DatabaseConfig, map[string]string, error) {
	driver := strings.ToLower(strings.TrimSpace(input.Driver))
	result := base.Database
	result.Driver = driver
	result.CredentialRef = ""
	if result.MaxOpenConns < 1 {
		result.MaxOpenConns = 4
	}
	if result.MaxIdleConns < 0 || result.MaxIdleConns > result.MaxOpenConns {
		result.MaxIdleConns = 2
	}
	if result.ConnMaxLifetimeMins < 0 {
		result.ConnMaxLifetimeMins = 30
	}
	switch driver {
	case "sqlite":
		if strings.TrimSpace(input.Host) != "" || input.Port != 0 || strings.TrimSpace(input.Database) != "" ||
			strings.TrimSpace(input.Username) != "" || input.Password != "" || strings.TrimSpace(input.RootCertificate) != "" {
			return config.DatabaseConfig{}, nil, errors.New("SQLite 使用系统管理的数据文件，不接受外部连接参数")
		}
		result.DSN = filepath.Join(base.Paths.Data, "scert.db")
		return result, nil, database.ValidateConfig(result)
	case "mysql", "postgres":
		// 继续进行外部数据库结构化字段校验。
	default:
		return config.DatabaseConfig{}, nil, fmt.Errorf("数据库类型无效：%s", input.Driver)
	}

	host, err := normalizeDatabaseHost(input.Host)
	if err != nil {
		return config.DatabaseConfig{}, nil, err
	}
	port := input.Port
	if port == 0 && driver == "mysql" {
		port = 3306
	}
	if port == 0 && driver == "postgres" {
		port = 5432
	}
	if port < 1 || port > 65535 {
		return config.DatabaseConfig{}, nil, fmt.Errorf("数据库端口无效：%d", port)
	}
	databaseName := strings.TrimSpace(input.Database)
	if !databaseNamePattern.MatchString(databaseName) {
		return config.DatabaseConfig{}, nil, errors.New("数据库名称格式无效")
	}
	username := strings.TrimSpace(input.Username)
	if username == "" || len(username) > 128 || containsControl(username) {
		return config.DatabaseConfig{}, nil, errors.New("数据库用户名格式无效")
	}
	if input.Password == "" || len(input.Password) > 4096 || containsControl(input.Password) {
		return config.DatabaseConfig{}, nil, errors.New("数据库密码不能为空且不能包含控制字符")
	}

	tlsMode := strings.ToLower(strings.TrimSpace(input.TLSMode))
	if driver == "mysql" {
		if tlsMode == "" {
			tlsMode = "true"
		}
		if tlsMode != "true" {
			return config.DatabaseConfig{}, nil, errors.New("MySQL 初始化连接必须启用证书和主机名校验，tls_mode 只能为 true")
		}
		if strings.TrimSpace(input.RootCertificate) != "" {
			return config.DatabaseConfig{}, nil, errors.New("当前 MySQL 初始化仅支持系统信任库，不接受自定义根证书路径")
		}
		mysqlConfig := mysql.NewConfig()
		mysqlConfig.Net = "tcp"
		mysqlConfig.Addr = net.JoinHostPort(host, strconv.Itoa(port))
		mysqlConfig.DBName = databaseName
		mysqlConfig.TLSConfig = "true"
		mysqlConfig.ParseTime = true
		mysqlConfig.Loc = time.UTC
		mysqlConfig.Params = map[string]string{"charset": "utf8mb4"}
		result.DSN = mysqlConfig.FormatDSN()
	} else {
		if tlsMode != "" && tlsMode != "verify-full" {
			return config.DatabaseConfig{}, nil, errors.New("PostgreSQL 初始化连接必须使用 sslmode=verify-full")
		}
		query := url.Values{"sslmode": []string{"verify-full"}}
		if root := strings.TrimSpace(input.RootCertificate); root != "" {
			if !filepath.IsAbs(root) {
				return config.DatabaseConfig{}, nil, errors.New("PostgreSQL 根证书路径必须是绝对路径")
			}
			query.Set("sslrootcert", filepath.Clean(root))
		}
		result.DSN = (&url.URL{
			Scheme: "postgres", Host: net.JoinHostPort(host, strconv.Itoa(port)),
			Path: "/" + databaseName, RawQuery: query.Encode(),
		}).String()
	}
	credentials := map[string]string{"username": username, "password": input.Password}
	credentialRef, err := database.NewCredentialReference()
	if err != nil {
		return config.DatabaseConfig{}, nil, err
	}
	result.CredentialRef = credentialRef
	if err := database.ValidateConfig(result); err != nil {
		return config.DatabaseConfig{}, nil, err
	}
	result.CredentialRef = ""
	return result, credentials, nil
}

func normalizeDatabaseHost(value string) (string, error) {
	host := strings.Trim(strings.TrimSpace(value), "[]")
	if host == "" || len(host) > 253 || containsControl(host) || strings.ContainsAny(host, `/\\@?#`) {
		return "", errors.New("数据库主机格式无效")
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("数据库主机格式无效")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
				return "", errors.New("数据库主机格式无效")
			}
		}
	}
	return host, nil
}

func containsControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
