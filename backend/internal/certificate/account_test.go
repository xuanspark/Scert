package certificate

import (
	"context"
	"testing"

	"github.com/go-acme/lego/v5/acme"
)

type memoryAccountStore struct {
	items map[string]map[string]string
}

func (s *memoryAccountStore) Put(_ context.Context, reference string, value map[string]string) error {
	if s.items == nil {
		s.items = map[string]map[string]string{}
	}
	copyValue := make(map[string]string, len(value))
	for key, item := range value {
		copyValue[key] = item
	}
	s.items[reference] = copyValue
	return nil
}

func (s *memoryAccountStore) Get(_ context.Context, reference string) (map[string]string, error) {
	return s.items[reference], nil
}

func (s *memoryAccountStore) Has(_ context.Context, reference string) (bool, error) {
	_, ok := s.items[reference]
	return ok, nil
}

func TestAccountRepositoryPersistsKeyAndRegistration(t *testing.T) {
	store := &memoryAccountStore{}
	repository := NewAccountRepository(store)
	user, err := repository.LoadOrCreate(context.Background(), "staging", "admin@example.com")
	if err != nil {
		t.Fatalf("创建 ACME 账户失败：%v", err)
	}
	if user.PrivateKey == nil {
		t.Fatal("ACME 账户私钥未生成")
	}
	user.Registration = &acme.ExtendedAccount{
		Account:  acme.Account{Status: acme.StatusValid, TermsOfServiceAgreed: true},
		Location: "https://acme.example.test/account/1",
	}
	if err := repository.Save(context.Background(), "staging", user); err != nil {
		t.Fatalf("保存 ACME 账户失败：%v", err)
	}
	loaded, err := repository.LoadOrCreate(context.Background(), "staging", "")
	if err != nil {
		t.Fatalf("重新读取 ACME 账户失败：%v", err)
	}
	if loaded.Email != "admin@example.com" {
		t.Fatalf("ACME 账户邮箱错误：%s", loaded.Email)
	}
	if loaded.Registration == nil || loaded.Registration.Location != user.Registration.Location {
		t.Fatalf("ACME 注册状态未持久化：%+v", loaded.Registration)
	}
	if loaded.PrivateKey == nil {
		t.Fatal("ACME 账户私钥未恢复")
	}
}

func TestAccountRepositorySeparatesEnvironments(t *testing.T) {
	store := &memoryAccountStore{}
	repository := NewAccountRepository(store)
	if _, err := repository.LoadOrCreate(context.Background(), "staging", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadOrCreate(context.Background(), "production", ""); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 2 {
		t.Fatalf("测试环境与生产环境应使用独立账户：%d", len(store.items))
	}
}
