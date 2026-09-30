package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type lifecycleDatabaseStore struct {
	memoryDatabaseStore
	afterPut     func()
	beforeDelete func(context.Context) error
	deleteCalls  atomic.Int32
}

func (s *lifecycleDatabaseStore) Put(ctx context.Context, reference string, values map[string]string) error {
	err := s.memoryDatabaseStore.Put(ctx, reference, values)
	if s.afterPut != nil {
		s.afterPut()
	}
	return err
}

func (s *lifecycleDatabaseStore) Delete(ctx context.Context, reference string) error {
	s.deleteCalls.Add(1)
	if s.beforeDelete != nil {
		if err := s.beforeDelete(ctx); err != nil {
			return err
		}
	}
	return s.memoryDatabaseStore.Delete(ctx, reference)
}

func databaseLifecycleInput() DatabaseInput {
	return DatabaseInput{
		Driver: "mysql", Host: "db.example.test", Database: "scert",
		Username: "owner", Password: "test-password",
	}
}

func TestDatabaseCancellationDoesNotPublishSession(t *testing.T) {
	for _, phase := range []string{"before", "after_put", "after_verify"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := &lifecycleDatabaseStore{}
			manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
			calls := 0
			manager.verify = func(context.Context, config.DatabaseConfig) error {
				calls++
				if phase == "after_verify" {
					cancel()
				}
				return nil
			}
			if phase == "before" {
				cancel()
			} else if phase == "after_put" {
				store.afterPut = cancel
			}
			result, err := manager.Verify(ctx, databaseLifecycleInput())
			if !errors.Is(err, context.Canceled) || result.VerificationToken != "" {
				t.Fatalf("取消请求不得发布令牌：%v", err)
			}
			if phase != "after_verify" && calls != 0 {
				t.Fatal("已取消请求仍执行了数据库连接验证")
			}
			if len(store.values) != 0 || len(manager.sessions) != 0 || manager.inFlight != 0 {
				t.Fatal("取消请求遗留了凭据、会话或并发占位")
			}
		})
	}
}

func TestDatabaseClaimCleanupFailureRetriesThroughPublicOperation(t *testing.T) {
	store := &memoryDatabaseStore{deleteFailures: 1}
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), databaseLifecycleInput())
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := manager.Claim(result.VerificationToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err == nil || len(manager.pendingCleanup) != 1 {
		t.Fatal("事务回收失败必须保留待清理引用")
	}
	manager.DiscardAll()
	if len(store.values) != 0 || len(manager.pendingCleanup) != 0 {
		t.Fatal("后续公开操作未重试事务遗留凭据")
	}
	if _, err := manager.Resolve(result.VerificationToken); err == nil {
		t.Fatal("事务已领取的令牌不得因清理重试重新生效")
	}
}

func TestDatabaseBatchCleanupProtectsExpiredFormalCredential(t *testing.T) {
	store := &memoryDatabaseStore{}
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), databaseLifecycleInput())
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return result.ExpiresAt }
	manager.DiscardAllExcept(result.CredentialRef)
	if _, exists := store.values[result.CredentialRef]; !exists {
		t.Fatal("批量清理错误删除了过期令牌关联的正式凭据")
	}
	if len(manager.sessions) != 0 || len(manager.expiredSession) != 0 {
		t.Fatal("保护正式凭据不应保留初始化令牌")
	}
}

func TestDatabaseCleaningSessionsReserveCapacity(t *testing.T) {
	for _, mode := range []string{"expiry", "discard", "batch"} {
		t.Run(mode, func(t *testing.T) {
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var startOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			store := &lifecycleDatabaseStore{beforeDelete: func(ctx context.Context) error {
				startOnce.Do(func() { close(started) })
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
			var verifyCalls atomic.Int32
			manager.verify = func(context.Context, config.DatabaseConfig) error { verifyCalls.Add(1); return nil }
			now := time.Now().UTC()
			manager.now = func() time.Time { return now }
			for i := range databaseVerificationCapacity {
				expires := now.Add(time.Hour)
				if mode == "expiry" && i == 0 {
					expires = now
				}
				manager.sessions[fmt.Sprint(i)] = databaseVerificationSession{
					CredentialRef: fmt.Sprint("credential-", i), ExpiresAt: expires,
				}
			}
			go func() {
				defer close(done)
				switch mode {
				case "expiry":
					_, _ = manager.Resolve("0")
				case "discard":
					_ = manager.Discard("0")
				case "batch":
					manager.DiscardAll()
				}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("凭据删除未开始")
			}
			if _, err := manager.Verify(t.Context(), DatabaseInput{Driver: "sqlite"}); err == nil {
				t.Fatal("清理中的会话未计入容量，错误接受了新验证")
			}
			if verifyCalls.Load() != 0 {
				t.Fatal("容量已满时仍进行了数据库验证")
			}
			unblock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("清理无法完成")
			}
			if len(manager.expiredSession) != 0 {
				t.Fatal("清理成功后仍保留容量占位")
			}
		})
	}
}

func TestDatabaseBatchDoesNotTakeOverConcurrentExpiryCleanup(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	store := &lifecycleDatabaseStore{beforeDelete: func(ctx context.Context) error {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), databaseLifecycleInput())
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return result.ExpiresAt }
	go func() { defer close(done); _, _ = manager.Resolve(result.VerificationToken) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("过期清理未开始")
	}
	batchDone := make(chan struct{})
	go func() { defer close(batchDone); manager.DiscardAll() }()
	select {
	case <-batchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("批量清理重复接管了正在删除的过期会话")
	}
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("过期清理未结束")
	}
	if store.deleteCalls.Load() != 1 || len(manager.expiredSession) != 0 {
		t.Fatal("单个过期会话被重复删除或遗留清理状态")
	}
}

func TestDatabaseBatchTakesOverFailedExpiryCleanup(t *testing.T) {
	store := &lifecycleDatabaseStore{}
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error { return nil }
	result, err := manager.Verify(t.Context(), databaseLifecycleInput())
	if err != nil {
		t.Fatal(err)
	}

	manager.mu.Lock()
	entry := expiredDatabaseVerificationSession{
		Token:   result.VerificationToken,
		Session: manager.sessions[result.VerificationToken],
	}
	delete(manager.sessions, result.VerificationToken)
	manager.expiredSession[result.VerificationToken] = entry
	manager.mu.Unlock()

	manager.DiscardAll()

	if store.deleteCalls.Load() != 1 {
		t.Fatalf("expected one cleanup attempt, got %d", store.deleteCalls.Load())
	}
	if len(manager.expiredSession) != 0 || len(manager.expiredCleaning) != 0 {
		t.Fatal("batch cleanup left expired state behind")
	}
	store.mu.Lock()
	_, retained := store.values[result.CredentialRef]
	store.mu.Unlock()
	if retained {
		t.Fatal("batch cleanup did not delete the credential")
	}
}

func TestDatabasePendingCleanupCountsTowardCapacity(t *testing.T) {
	store := &lifecycleDatabaseStore{beforeDelete: func(context.Context) error { return errors.New("测试删除失败") }}
	manager := NewDatabaseVerificationManager(config.Default(t.TempDir()), store)
	manager.verify = func(context.Context, config.DatabaseConfig) error {
		t.Fatal("待回收凭据占满容量时不应连接数据库")
		return nil
	}
	for i := range databaseVerificationCapacity {
		manager.pendingCleanup[fmt.Sprint("pending-", i)] = struct{}{}
	}
	if _, err := manager.Verify(t.Context(), DatabaseInput{Driver: "sqlite"}); err == nil {
		t.Fatal("未回收凭据未计入验证容量")
	}
	if manager.inFlight != 0 || len(manager.pendingCleanup) != databaseVerificationCapacity {
		t.Fatal("拒绝验证时破坏了待清理状态")
	}
}
