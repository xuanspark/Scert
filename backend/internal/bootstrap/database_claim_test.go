package bootstrap

import (
	"context"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestDatabaseClaimProtectsCredentialUntilTransactionCleanup(t *testing.T) {
	store := newCompletionMemorySecrets()
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert", Username: "owner", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, cleanup, err := manager.Claim(result.VerificationToken)
	if err != nil || session.CredentialRef != result.CredentialRef || cleanup == nil {
		t.Fatalf("领取数据库凭据失败：%v", err)
	}
	manager.now = func() time.Time { return result.ExpiresAt.Add(time.Second) }
	if _, err := manager.Resolve(result.VerificationToken); err == nil {
		t.Fatal("已领取令牌仍可读取")
	}
	if _, _, err := manager.Claim(result.VerificationToken); err == nil {
		t.Fatal("已领取令牌仍可重复领取")
	}
	manager.DiscardAll()
	if !store.has(result.CredentialRef) {
		t.Fatal("过期或批量清理误删了事务持有的数据库凭据")
	}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			if err := cleanup(); err != nil {
				t.Errorf("事务回收失败：%v", err)
			}
		})
	}
	workers.Wait()
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.values) != 0 || len(store.deleted) != 1 {
		t.Fatalf("事务回收必须且只能删除一次：剩余=%d，删除=%d", len(store.values), len(store.deleted))
	}
}

func TestDatabaseClaimHasOnlyOneConcurrentOwner(t *testing.T) {
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), newCompletionMemorySecrets())
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{Driver: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	owners := make(chan func() error, 32)
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			if _, cleanup, err := manager.Claim(result.VerificationToken); err == nil {
				owners <- cleanup
			}
		})
	}
	workers.Wait()
	close(owners)
	count := 0
	for cleanup := range owners {
		count++
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("数据库验证结果存在多个事务拥有者：%d", count)
	}
}

func TestDatabaseClaimRejectsExpiredToken(t *testing.T) {
	store := newCompletionMemorySecrets()
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), DatabaseInput{
		Driver: "postgres", Host: "db.example.test", Database: "scert", Username: "owner", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return result.ExpiresAt }
	if _, cleanup, err := manager.Claim(result.VerificationToken); err == nil || cleanup != nil {
		t.Fatal("已过期的数据库令牌不得被领取")
	}
	if store.has(result.CredentialRef) {
		t.Fatal("过期且未领取的数据库凭据未回收")
	}
}
