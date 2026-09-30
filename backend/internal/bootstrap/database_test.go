package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type memoryDatabaseStore struct {
	mu             sync.Mutex
	values         map[string]map[string]string
	putFailure     bool
	deleteFailures int
}

func (s *memoryDatabaseStore) Put(_ context.Context, reference string, value map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]map[string]string)
	}
	s.values[reference] = map[string]string{"username": value["username"], "password": value["password"]}
	if s.putFailure {
		return errors.New("simulated put failure")
	}
	return nil
}

func (s *memoryDatabaseStore) Get(_ context.Context, reference string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[reference], nil
}

func (s *memoryDatabaseStore) Delete(_ context.Context, reference string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteFailures > 0 {
		s.deleteFailures--
		return errors.New("simulated delete failure")
	}
	delete(s.values, reference)
	return nil
}

func TestBuildDatabaseConfigSupportsThreeStorageModesWithoutPersistingCredentials(t *testing.T) {
	base := config.Default(t.TempDir())
	base.Paths.Data = filepath.Join(t.TempDir(), "data")
	tests := []struct {
		name  string
		input DatabaseInput
	}{
		{name: "SQLite", input: DatabaseInput{Driver: "sqlite"}},
		{name: "MySQL", input: DatabaseInput{Driver: "mysql", Host: "db.example.test", Database: "scert", Username: "scert_user", Password: "mysql-secret", TLSMode: "true"}},
		{name: "PostgreSQL", input: DatabaseInput{Driver: "postgres", Host: "db.example.test", Database: "scert", Username: "scert_user", Password: "postgres-secret", TLSMode: "verify-full"}},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			persisted, credentials, err := buildDatabaseConfig(base, item.input)
			if err != nil {
				t.Fatalf("构造数据库配置失败：%v", err)
			}
			if (item.input.Username != "" && strings.Contains(persisted.DSN, item.input.Username)) ||
				(item.input.Password != "" && strings.Contains(persisted.DSN, item.input.Password)) {
				t.Fatalf("持久化数据库配置泄露账号或密码：%s", persisted.DSN)
			}
			if item.input.Driver == "sqlite" {
				if len(credentials) != 0 || persisted.DSN != filepath.Join(base.Paths.Data, "scert.db") {
					t.Fatalf("SQLite 配置不正确：%+v credentials=%v", persisted, credentials)
				}
				return
			}
			if credentials["username"] != item.input.Username || credentials["password"] != item.input.Password {
				t.Fatal("外部数据库凭据未单独返回")
			}
		})
	}
}

func TestDatabaseVerificationStoresCredentialsAndConsumesTokenOnce(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(_ context.Context, runtime config.DatabaseConfig) error {
		if !strings.Contains(runtime.DSN, "database-password") {
			t.Fatalf("运行时连接信息未注入内存密码：%s", runtime.DSN)
		}
		return nil
	}
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatalf("数据库验证失败：%v", err)
	}
	if result.CredentialRef == "" || result.VerificationToken == "" {
		t.Fatal("数据库验证结果缺少加密凭据引用或令牌")
	}
	if strings.Contains(result.Config.DSN, "scert_user") || strings.Contains(result.Config.DSN, "database-password") {
		t.Fatalf("数据库验证响应泄露凭据：%+v", result.Config)
	}
	consumed, err := manager.Consume(result.VerificationToken, result.CredentialRef)
	if err != nil || consumed.CredentialRef != result.CredentialRef {
		t.Fatalf("消费数据库验证令牌失败：%v config=%+v", err, consumed)
	}
	if _, err := manager.Consume(result.VerificationToken, result.CredentialRef); err == nil {
		t.Fatal("数据库验证令牌被重复消费")
	}
	store.mu.Lock()
	_, retained := store.values[result.CredentialRef]
	store.mu.Unlock()
	if !retained {
		t.Fatal("已消费的正式数据库凭据不应被验证令牌管理器删除")
	}
}

func TestDatabaseVerificationFailureAndExpiryDeleteTemporaryCredentials(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return context.DeadlineExceeded }
	if _, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "postgres", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "verify-full",
	}); err == nil {
		t.Fatal("数据库验证失败未返回错误")
	}
	store.mu.Lock()
	count := len(store.values)
	store.mu.Unlock()
	if count != 0 {
		t.Fatalf("失败的数据库验证遗留临时凭据：%d", count)
	}

	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	now := time.Now().UTC()
	manager.now = func() time.Time { return now }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return now.Add(databaseVerificationLifetime + time.Second) }
	if _, err := manager.Check(result.VerificationToken, result.CredentialRef); err == nil {
		t.Fatal("过期数据库验证令牌未被拒绝")
	}
	store.mu.Lock()
	_, exists := store.values[result.CredentialRef]
	store.mu.Unlock()
	if exists {
		t.Fatal("过期数据库验证令牌的临时凭据未被删除")
	}
}

func TestSQLiteRejectsUserControlledPathAndExternalFields(t *testing.T) {
	base := config.Default(t.TempDir())
	if _, _, err := buildDatabaseConfig(base, DatabaseInput{Driver: "sqlite", Database: "other.db"}); err == nil {
		t.Fatal("SQLite 用户自定义路径未被拒绝")
	}
}

func TestDatabaseVerificationDiscardOnlyRemovesSelectedToken(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }

	first, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "first_user", Password: "first-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "postgres", Host: "db.example.test", Database: "scert",
		Username: "second_user", Password: "second-password", TLSMode: "verify-full",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Discard(first.VerificationToken); err != nil {
		t.Fatalf("精确回收数据库验证令牌失败：%v", err)
	}
	if _, err := manager.Check(first.VerificationToken, first.CredentialRef); err == nil {
		t.Fatal("已回收的数据库验证令牌仍然有效")
	}
	if _, err := manager.Check(second.VerificationToken, second.CredentialRef); err != nil {
		t.Fatalf("精确回收影响了其他数据库验证令牌：%v", err)
	}
	store.mu.Lock()
	_, firstExists := store.values[first.CredentialRef]
	_, secondExists := store.values[second.CredentialRef]
	store.mu.Unlock()
	if firstExists || !secondExists {
		t.Fatalf("数据库凭据回收范围错误：first=%v second=%v", firstExists, secondExists)
	}
	if err := manager.Discard(first.VerificationToken); err == nil {
		t.Fatal("数据库验证令牌被重复回收")
	}
}

func TestDatabaseVerificationDiscardAllExceptProtectsFormalCredential(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }

	protected, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "formal_user", Password: "formal-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "postgres", Host: "db.example.test", Database: "scert",
		Username: "pending_user", Password: "pending-password", TLSMode: "verify-full",
	})
	if err != nil {
		t.Fatal(err)
	}

	manager.DiscardAllExcept("  " + protected.CredentialRef + "  ")

	if _, err := manager.Resolve(protected.VerificationToken); err == nil {
		t.Fatal("批量清理后受保护凭据对应的验证令牌仍然有效")
	}
	if _, err := manager.Resolve(pending.VerificationToken); err == nil {
		t.Fatal("批量清理后待处理凭据对应的验证令牌仍然有效")
	}
	store.mu.Lock()
	_, protectedExists := store.values[protected.CredentialRef]
	_, pendingExists := store.values[pending.CredentialRef]
	store.mu.Unlock()
	if !protectedExists || pendingExists {
		t.Fatalf("数据库凭据保护范围错误：protected=%v pending=%v", protectedExists, pendingExists)
	}
}

func TestDatabaseVerificationResolveIsServerOnlyAndDoesNotConsumeToken(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.Resolve(result.VerificationToken)
	if err != nil {
		t.Fatalf("按令牌解析数据库验证结果失败：%v", err)
	}
	if resolved.Config.CredentialRef != result.CredentialRef || resolved.CredentialRef != result.CredentialRef || resolved.ExpiresAt != result.ExpiresAt {
		t.Fatalf("解析的数据库验证状态不完整：%+v", resolved)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), result.CredentialRef) || strings.Contains(string(encoded), result.Config.DSN) {
		t.Fatalf("服务端数据库验证状态可被意外序列化：%s", encoded)
	}
	if _, err := manager.Consume(result.VerificationToken, result.CredentialRef); err != nil {
		t.Fatalf("Resolve 不应消费数据库验证令牌：%v", err)
	}
	if _, err := manager.Resolve(result.VerificationToken); err == nil {
		t.Fatal("已消费的数据库验证令牌仍可被解析")
	}
}

func TestDatabaseVerificationConsumesTokenOnlyOnceUnderConcurrency(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, consumeErr := manager.Consume(result.VerificationToken, result.CredentialRef)
			results <- consumeErr
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
		t.Fatalf("并发消费数据库验证令牌成功次数为 %d，预期为 1", successes)
	}
}

func TestDatabaseVerificationPutFailureQueuesCredentialCleanup(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{putFailure: true, deleteFailures: 1}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	_, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "true",
	})
	if err == nil {
		t.Fatal("expected credential persistence failure")
	}
	store.mu.Lock()
	retainedAfterFailure := len(store.values)
	store.mu.Unlock()
	if retainedAfterFailure != 1 {
		t.Fatalf("expected failed persistence cleanup to retain one credential temporarily, got %d", retainedAfterFailure)
	}
	manager.retryPendingCredentialCleanup()
	store.mu.Lock()
	remaining := len(store.values)
	store.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected pending credential cleanup to succeed on retry, got %d remaining", remaining)
	}
}

func TestDatabaseVerificationDiscardFailureRestoresTokenForRetry(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{deleteFailures: 1}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Discard(result.VerificationToken); err == nil {
		t.Fatal("expected first credential discard to fail")
	}
	if _, err := manager.Resolve(result.VerificationToken); err != nil {
		t.Fatalf("expected failed discard to restore the verification token: %v", err)
	}
	store.mu.Lock()
	_, retained := store.values[result.CredentialRef]
	store.mu.Unlock()
	if !retained {
		t.Fatal("credential must remain available while the token is restored")
	}
	if err := manager.Discard(result.VerificationToken); err != nil {
		t.Fatalf("expected retry discard to succeed: %v", err)
	}
	if _, err := manager.Resolve(result.VerificationToken); err == nil {
		t.Fatal("discarded token must not remain usable")
	}
	store.mu.Lock()
	_, retained = store.values[result.CredentialRef]
	store.mu.Unlock()
	if retained {
		t.Fatal("successfully discarded credential must be deleted")
	}
}

func TestDatabaseVerificationExpiredCleanupFailureRestoresTokenForRetry(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{deleteFailures: 1}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "postgres", Host: "db.example.test", Database: "scert",
		Username: "scert_user", Password: "database-password", TLSMode: "verify-full",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return result.ExpiresAt }
	if _, err := manager.Check(result.VerificationToken, result.CredentialRef); err == nil {
		t.Fatal("expired token must be rejected even when its credential cleanup fails")
	}
	manager.mu.Lock()
	_, restored := manager.sessions[result.VerificationToken]
	manager.mu.Unlock()
	if !restored {
		t.Fatal("failed expiry cleanup must restore the session for a later retry")
	}
	store.mu.Lock()
	_, retained := store.values[result.CredentialRef]
	store.mu.Unlock()
	if !retained {
		t.Fatal("credential must remain available after failed expiry cleanup")
	}
	if _, err := manager.Check(result.VerificationToken, result.CredentialRef); err == nil {
		t.Fatal("expired token must be invalid after retry cleanup succeeds")
	}
	store.mu.Lock()
	_, retained = store.values[result.CredentialRef]
	store.mu.Unlock()
	if retained {
		t.Fatal("successfully cleaned expired credential must be deleted")
	}
}

func TestDatabaseVerificationDiscardAllRestoresOnlyFailedSession(t *testing.T) {
	base := config.Default(t.TempDir())
	store := &memoryDatabaseStore{deleteFailures: 1}
	manager := NewDatabaseVerificationManager(base, store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	first, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "first", Password: "first-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "second", Password: "second-password", TLSMode: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.DiscardAll()
	valid := 0
	for _, token := range []string{first.VerificationToken, second.VerificationToken} {
		if _, err := manager.Resolve(token); err == nil {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("expected exactly one failed batch cleanup to restore its token, got %d", valid)
	}
	manager.DiscardAll()
	store.mu.Lock()
	remaining := len(store.values)
	store.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected second batch cleanup to remove the restored credential, got %d remaining", remaining)
	}
}
