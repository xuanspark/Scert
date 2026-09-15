package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

const readinessDNSReference = "secret://dns/aliyun/readiness-test"

func TestReadinessRejectsIncompleteSetup(t *testing.T) {
	cfg := config.Default(t.TempDir())
	response := requestReadiness(New(Dependencies{Config: cfg}))
	assertReadinessResponse(t, response, http.StatusServiceUnavailable, "setup_incomplete")
}

func TestReadinessRejectsMissingDNSCredentialReference(t *testing.T) {
	cfg := readinessConfig(t)
	cfg.DNS.CredentialRef = ""
	response := requestReadiness(New(Dependencies{Config: cfg}))
	assertReadinessResponse(t, response, http.StatusServiceUnavailable, "dns_credential_missing")
}

func TestReadinessRejectsUnavailableCredentialWithoutLeakingReference(t *testing.T) {
	cfg := readinessConfig(t)
	store, err := secretstore.Open(filepath.Join(t.TempDir(), "secrets"))
	if err != nil {
		t.Fatalf("打开测试敏感信息存储失败：%v", err)
	}
	response := requestReadiness(New(Dependencies{Config: cfg, Secrets: store}))
	assertReadinessResponse(t, response, http.StatusServiceUnavailable, "credential_unavailable")
	if strings.Contains(response.Body.String(), readinessDNSReference) || strings.Contains(response.Body.String(), "secret://") {
		t.Fatalf("生产就绪响应泄漏敏感信息引用：%s", response.Body.String())
	}
}

func TestReadinessChecksConfiguredNotificationCredentials(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*config.Config)
	}{
		{
			name: "HTTP notification",
			configure: func(cfg *config.Config) {
				cfg.Notification.HTTPCredentialRef = "secret://notification/http/readiness-test"
			},
		},
		{
			name: "SMTP notification",
			configure: func(cfg *config.Config) {
				cfg.Notification.SMTPCredentialRef = "secret://notification/smtp/readiness-test"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := readinessConfig(t)
			test.configure(&cfg)
			store := readinessStore(t)
			response := requestReadiness(New(Dependencies{Config: cfg, Secrets: store}))
			assertReadinessResponse(t, response, http.StatusServiceUnavailable, "credential_unavailable")
		})
	}
}

func TestReadinessRejectsInvalidTLSMaterial(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, *config.Config)
	}{
		{
			name: "missing file",
			configure: func(t *testing.T, cfg *config.Config) {
				cfg.Server.TLSCert = filepath.Join(t.TempDir(), "missing.pem")
			},
		},
		{
			name: "directory",
			configure: func(t *testing.T, cfg *config.Config) {
				cfg.Server.TLSKey = t.TempDir()
			},
		},
		{
			name: "symbolic link",
			configure: func(t *testing.T, cfg *config.Config) {
				target := filepath.Join(t.TempDir(), "target.pem")
				if err := os.WriteFile(target, []byte("test"), 0o600); err != nil {
					t.Fatalf("写入符号链接目标失败：%v", err)
				}
				link := filepath.Join(t.TempDir(), "linked.pem")
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("当前环境无法创建符号链接：%v", err)
				}
				cfg.Server.TLSCert = link
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := readinessConfig(t)
			test.configure(t, &cfg)
			response := requestReadiness(New(Dependencies{Config: cfg, Secrets: readinessStore(t)}))
			assertReadinessResponse(t, response, http.StatusServiceUnavailable, "tls_material_unavailable")
		})
	}
}

func TestReadinessRejectsUnavailableDatabase(t *testing.T) {
	cfg := readinessConfig(t)
	response := requestReadiness(New(Dependencies{
		Config:  cfg,
		Secrets: readinessStore(t),
		ReadinessCheck: func(_ context.Context) error {
			return errors.New("database unavailable")
		},
	}))
	assertReadinessResponse(t, response, http.StatusServiceUnavailable, "database_unavailable")
}

func TestReadinessSucceedsWhenDependenciesAreAvailable(t *testing.T) {
	cfg := readinessConfig(t)
	response := requestReadiness(New(Dependencies{
		Config:  cfg,
		Secrets: readinessStore(t),
		ReadinessCheck: func(_ context.Context) error {
			return nil
		},
	}))
	assertReadinessResponse(t, response, http.StatusOK, "ready")
}

func TestReadinessIsNotHandledByFrontendFallback(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("frontend fallback"), 0o644); err != nil {
		t.Fatalf("写入前端入口失败：%v", err)
	}
	cfg := config.Default(t.TempDir())
	response := requestReadiness(New(Dependencies{Config: cfg, FrontendDir: staticDir}))
	assertReadinessResponse(t, response, http.StatusServiceUnavailable, "setup_incomplete")
	if strings.Contains(response.Body.String(), "frontend fallback") {
		t.Fatalf("生产就绪路径被前端回退接管：%s", response.Body.String())
	}
}

func readinessConfig(t *testing.T) config.Config {
	t.Helper()
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.DNS.Provider = "aliyun"
	cfg.DNS.CredentialRef = readinessDNSReference
	cfg.Server.TLSCert = filepath.Join(directory, "server.crt")
	cfg.Server.TLSKey = filepath.Join(directory, "server.key")
	if err := os.WriteFile(cfg.Server.TLSCert, []byte("certificate"), 0o644); err != nil {
		t.Fatalf("写入测试证书失败：%v", err)
	}
	if err := os.WriteFile(cfg.Server.TLSKey, []byte("private key"), 0o600); err != nil {
		t.Fatalf("写入测试私钥失败：%v", err)
	}
	return cfg
}

func readinessStore(t *testing.T) *secretstore.Store {
	t.Helper()
	store, err := secretstore.Open(filepath.Join(t.TempDir(), "secrets"))
	if err != nil {
		t.Fatalf("打开测试敏感信息存储失败：%v", err)
	}
	if err := store.Put(t.Context(), readinessDNSReference, map[string]string{
		"access_key_id":     "readiness-test-id",
		"access_key_secret": "readiness-test-secret",
	}); err != nil {
		t.Fatalf("写入测试 DNS 凭据失败：%v", err)
	}
	return store
}

func requestReadiness(server *Server) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return response
}

func assertReadinessResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantState string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("生产就绪状态码 = %d，期望 %d，响应：%s", response.Code, wantStatus, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"`+wantState+`"`) &&
		!strings.Contains(response.Body.String(), `"reason":"`+wantState+`"`) {
		t.Fatalf("生产就绪响应缺少状态 %q：%s", wantState, response.Body.String())
	}
}
