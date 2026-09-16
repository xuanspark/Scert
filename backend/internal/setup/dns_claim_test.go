package setup

import (
	"sync"
	"testing"
	"time"
)

func TestDNSClaimProtectsCredentialUntilTransactionCleanup(t *testing.T) {
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(fakeDNSTester{}, store)
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "aliyun", TestDomain: "admin.example.test",
		Credentials: map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, cleanup, err := manager.Claim(result.VerificationToken)
	if err != nil || cleanup == nil || session.CredentialRef != result.CredentialRef || session.TestDomain != "admin.example.test" {
		t.Fatalf("领取 DNS 凭据失败：%v", err)
	}
	manager.now = func() time.Time { return result.ExpiresAt.Add(time.Second) }
	if _, err := manager.Resolve(result.VerificationToken); err == nil {
		t.Fatal("已领取 DNS 令牌仍可读取")
	}
	if _, _, err := manager.Claim(result.VerificationToken); err == nil {
		t.Fatal("已领取 DNS 令牌仍可重复领取")
	}
	manager.DiscardAll()
	store.mu.Lock()
	_, exists := store.values[result.CredentialRef]
	store.mu.Unlock()
	if !exists {
		t.Fatal("过期或批量清理误删了事务持有的 DNS 凭据")
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
		t.Fatalf("DNS 事务回收必须且只能删除一次：剩余=%d，删除=%d", len(store.values), len(store.deleted))
	}
}

func TestDNSClaimHasOnlyOneConcurrentOwner(t *testing.T) {
	manager := NewDNSVerificationManager(fakeDNSTester{}, &memorySecretWriter{})
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "aliyun", TestDomain: "admin.example.test",
		Credentials: map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"},
	})
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
		t.Fatalf("DNS 验证结果存在多个事务拥有者：%d", count)
	}
}

func TestDNSClaimRejectsExpiredToken(t *testing.T) {
	store := &memorySecretWriter{}
	manager := NewDNSVerificationManager(fakeDNSTester{}, store)
	result, err := manager.Verify(t.Context(), DNSVerificationInput{
		ProviderID: "aliyun", TestDomain: "admin.example.test",
		Credentials: map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return result.ExpiresAt }
	if _, cleanup, err := manager.Claim(result.VerificationToken); err == nil || cleanup != nil {
		t.Fatal("已过期的 DNS 令牌不得被领取")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.values) != 0 {
		t.Fatal("过期且未领取的 DNS 凭据未回收")
	}
}
