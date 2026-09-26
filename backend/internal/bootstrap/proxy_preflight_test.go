package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestProxyRejectsUntrustedModeBeforeConsumingVerification(t *testing.T) {
	for _, mode := range []string{"staging", "ip"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCompletionFixture(t, "admin.example.test")
			request := fixture.request("admin.example.test")
			request.HTTPSMode = "proxy"
			if mode == "ip" {
				request.WebHostname = ""
				request.WebPublicIP = "203.0.113.10"
				request.ACMEEnvironment = "production"
			}
			_, err := fixture.completer(nil).Complete(context.Background(), request)
			var failure *CompleteError
			if !errors.As(err, &failure) || failure.Kind != CompleteErrorProxyTrust || fixture.issuer.calls != 0 {
				t.Fatalf("信任模式未提前拒绝：%v", err)
			}
			if _, _, err := fixture.database.Claim(fixture.databaseToken); err != nil {
				t.Fatalf("数据库令牌被消耗：%v", err)
			}
			if _, _, err := fixture.dns.Claim(fixture.dnsToken); err != nil {
				t.Fatalf("DNS 令牌被消耗：%v", err)
			}
		})
	}
}

func TestProxyFailureDoesNotCommitInitialization(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	request := fixture.request("admin.example.test")
	request.HTTPSMode = "proxy"
	request.ACMEEnvironment = "production"
	checked := false
	_, err := fixture.completer(func(deps *CompleteDependencies) {
		deps.VerifyProxy = func(_ context.Context, candidate config.Config) error {
			checked = candidate.Server.TLSCert != "" && candidate.Server.TLSKey != ""
			return errors.New("secret diagnostic")
		}
	}).Complete(context.Background(), request)
	var failure *CompleteError
	if !checked || !errors.As(err, &failure) || failure.Kind != CompleteErrorProxyReadiness {
		t.Fatalf("未验证候选代理：%v", err)
	}
	if _, err := os.Stat(fixture.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("代理失败仍提交了初始化配置")
	}
	response := httptest.NewRecorder()
	writeBootstrapCompletionError(response, failure)
	if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body.String(), "secret") {
		t.Fatal("代理错误未脱敏")
	}
}

func TestDirectModeDoesNotRequireProxy(t *testing.T) {
	if err := verifySetupProxy(context.Background(), config.Config{Server: config.ServerConfig{HTTPSMode: "direct"}}); err != nil {
		t.Fatal(err)
	}
}
