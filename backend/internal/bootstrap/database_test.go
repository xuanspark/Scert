package bootstrap

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type memoryDatabaseStore struct {
	mu     sync.Mutex
	values map[string]map[string]string
}

func (s *memoryDatabaseStore) Put(_ context.Context, reference string, value map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]map[string]string)
	}
	s.values[reference] = map[string]string{"username": value["username"], "password": value["password"]}
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
