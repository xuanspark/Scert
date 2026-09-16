package firewall

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFirewallModeRequiresExplicitExternalPolicy(t *testing.T) {
	for _, mode := range []string{"", "auto", " AUTO "} {
		selected, err := NewManagerWithMode(mode)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := selected.(*manager); !ok {
			t.Fatal("默认模式必须保留自动管理，不能悄悄降级")
		}
	}
	for _, mode := range []string{"disabled", "skip", "external;auto"} {
		if _, err := NewManagerWithMode(mode); err == nil {
			t.Fatal("未知防火墙模式必须拒绝")
		}
	}
}

func TestExternalFirewallNeverClaimsRulesWereVerifiedOrCreated(t *testing.T) {
	selected, err := NewManagerWithMode(" EXTERNAL ")
	if err != nil {
		t.Fatal(err)
	}
	result, err := selected.OpenTCP(context.Background(), 8443)
	if err != nil || result.Created || result.Backend != BackendExternal || result.Port != 8443 ||
		!strings.Contains(result.Message, "未检查或修改") || !strings.Contains(result.Message, "8443") {
		t.Fatalf("外部管理结果不得冒充规则验证成功：%+v，%v", result, err)
	}
	if err := selected.Rollback(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	result.Created = true
	if err := selected.Rollback(context.Background(), result); err == nil {
		t.Fatal("外部管理器不能操作其他管理器的规则")
	}
	for _, port := range []int{-1, 0, 65536} {
		if _, err := selected.OpenTCP(context.Background(), port); err == nil {
			t.Fatal("外部管理模式也必须拒绝无效端口")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := selected.OpenTCP(ctx, 8443); !errors.Is(err, context.Canceled) {
		t.Fatal("取消请求不应继续执行")
	}
}
