package dnsprovider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type Factory interface {
	Create(providerID string, credentials map[string]string) (challenge.Provider, error)
}

type Tester struct {
	factory Factory
}

func NewTester(factory Factory) *Tester {
	return &Tester{factory: factory}
}

func (t *Tester) Verify(ctx context.Context, providerID, testDomain string, credentials map[string]string) error {
	if t == nil || t.factory == nil {
		return errors.New("DNS 验证服务未初始化")
	}
	testDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(testDomain), "."))
	if !domain.IsValidName(testDomain) {
		return fmt.Errorf("DNS 测试域名格式无效：%s", testDomain)
	}
	cleaned, err := ValidateCredentials(providerID, credentials)
	if err != nil {
		return err
	}
	provider, err := t.factory.Create(providerID, cleaned)
	if err != nil {
		return err
	}
	token, err := randomValue(24)
	if err != nil {
		return fmt.Errorf("生成 DNS 测试标识失败：%w", err)
	}
	keyAuth, err := randomValue(48)
	if err != nil {
		return fmt.Errorf("生成 DNS 测试授权值失败：%w", err)
	}
	if err := provider.Present(ctx, testDomain, token, keyAuth); err != nil {
		return fmt.Errorf("创建 DNS 测试记录失败：%w", err)
	}
	if err := provider.CleanUp(ctx, testDomain, token, keyAuth); err != nil {
		return fmt.Errorf("DNS 测试记录已创建，但自动清理失败：%w", err)
	}
	return nil
}

func randomValue(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
