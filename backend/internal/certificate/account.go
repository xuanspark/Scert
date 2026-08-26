package certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
)

type AccountSecretStore interface {
	Put(ctx context.Context, reference string, value map[string]string) error
	Get(ctx context.Context, reference string) (map[string]string, error)
	Has(ctx context.Context, reference string) (bool, error)
}

type AccountRepository struct {
	store AccountSecretStore
}

func NewAccountRepository(store AccountSecretStore) *AccountRepository {
	return &AccountRepository{store: store}
}

func (r *AccountRepository) LoadOrCreate(ctx context.Context, environment, email string) (*AccountUser, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("ACME 账户存储未初始化")
	}
	reference, err := accountReference(environment)
	if err != nil {
		return nil, err
	}
	exists, err := r.store.Has(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("检查 ACME 账户失败：%w", err)
	}
	if !exists {
		key, err := certcrypto.GeneratePrivateKey(certcrypto.EC256)
		if err != nil {
			return nil, fmt.Errorf("生成 ACME 账户私钥失败：%w", err)
		}
		user := &AccountUser{Email: strings.TrimSpace(email), PrivateKey: key}
		if err := r.Save(ctx, environment, user); err != nil {
			return nil, err
		}
		return user, nil
	}

	payload, err := r.store.Get(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("读取 ACME 账户失败：%w", err)
	}
	key, err := certcrypto.ParsePEMPrivateKey([]byte(payload["private_key"]))
	if err != nil {
		return nil, fmt.Errorf("解析 ACME 账户私钥失败：%w", err)
	}
	storedEmail := strings.TrimSpace(payload["email"])
	if strings.TrimSpace(email) != "" {
		storedEmail = strings.TrimSpace(email)
	}
	user := &AccountUser{Email: storedEmail, PrivateKey: key}
	if value := strings.TrimSpace(payload["registration"]); value != "" {
		var registration acme.ExtendedAccount
		if err := json.Unmarshal([]byte(value), &registration); err != nil {
			return nil, fmt.Errorf("解析 ACME 注册状态失败：%w", err)
		}
		user.Registration = &registration
	}
	return user, nil
}

func (r *AccountRepository) Save(ctx context.Context, environment string, user *AccountUser) error {
	if r == nil || r.store == nil {
		return errors.New("ACME 账户存储未初始化")
	}
	if user == nil || user.PrivateKey == nil {
		return errors.New("ACME 账户私钥未配置")
	}
	reference, err := accountReference(environment)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"email":       strings.TrimSpace(user.Email),
		"private_key": string(certcrypto.PEMEncode(user.PrivateKey)),
	}
	if user.Registration != nil {
		value, err := json.Marshal(user.Registration)
		if err != nil {
			return fmt.Errorf("编码 ACME 注册状态失败：%w", err)
		}
		payload["registration"] = string(value)
	}
	if err := r.store.Put(ctx, reference, payload); err != nil {
		return fmt.Errorf("保存 ACME 账户失败：%w", err)
	}
	return nil
}

func accountReference(environment string) (string, error) {
	environment = strings.ToLower(strings.TrimSpace(environment))
	if environment != "staging" && environment != "production" {
		return "", fmt.Errorf("ACME 环境无效：%s", environment)
	}
	return "secret://acme/letsencrypt/" + environment, nil
}
