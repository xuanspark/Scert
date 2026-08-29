package certificate

import (
	"context"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/challenge"
)

type propagationTestProvider struct {
	present bool
	cleanup bool
}

func (p *propagationTestProvider) Present(context.Context, string, string, string) error {
	p.present = true
	return nil
}

func (p *propagationTestProvider) CleanUp(context.Context, string, string, string) error {
	p.cleanup = true
	return nil
}

func TestWithPropagationSettingsProvidesConfiguredTimeout(t *testing.T) {
	base := &propagationTestProvider{}
	timeout := 15 * time.Minute
	interval := 10 * time.Second
	wrapped := withPropagationSettings(base, timeout, interval)
	provider, ok := wrapped.(challenge.ProviderTimeout)
	if !ok {
		t.Fatal("DNS Provider 未实现传播等待配置接口")
	}
	actualTimeout, actualInterval := provider.Timeout()
	if actualTimeout != timeout || actualInterval != interval {
		t.Fatalf("DNS 传播参数不正确：timeout=%s interval=%s", actualTimeout, actualInterval)
	}
	if err := wrapped.Present(context.Background(), "example.com", "token", "key-auth"); err != nil {
		t.Fatalf("Provider Present 转发失败：%v", err)
	}
	if err := wrapped.CleanUp(context.Background(), "example.com", "token", "key-auth"); err != nil {
		t.Fatalf("Provider CleanUp 转发失败：%v", err)
	}
	if !base.present || !base.cleanup {
		t.Fatal("包装器未正确转发 Provider 操作")
	}
}

func TestWithPropagationSettingsPreservesProviderForInvalidValues(t *testing.T) {
	base := &propagationTestProvider{}
	cases := []struct {
		name     string
		timeout  time.Duration
		interval time.Duration
	}{
		{name: "zero timeout", timeout: 0, interval: time.Second},
		{name: "zero interval", timeout: time.Minute, interval: 0},
		{name: "interval exceeds timeout", timeout: time.Second, interval: time.Minute},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := withPropagationSettings(base, item.timeout, item.interval); got != base {
				t.Fatal("无效 DNS 传播参数不应替换原 Provider")
			}
		})
	}
}

func TestWithPropagationSettingsPreservesNilProvider(t *testing.T) {
	if got := withPropagationSettings(nil, time.Minute, time.Second); got != nil {
		t.Fatal("空 Provider 应保持为空")
	}
}
