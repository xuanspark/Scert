package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func prepareDesktopTransition(t *testing.T) (*Runtime, http.Handler, string, func()) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "config", "scert.json")
	runtime, err := Open(path, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	cfg := defaultConfig(root)
	if err := setup.Apply(&cfg, setup.Input{
		AdminUsername: "test-admin", AdminEmail: "admin@example.test", AdminPassword: "Test1234!",
		ServerPort: 8080, WebHostname: "scert.example.test", WebEnabled: true,
		DNSProvider: "aliyun", DNSCredentialRef: "secret://dns/test", DNSVerified: true,
		CertificateRoot: cfg.Paths.Certificate, HTTPSMode: "direct",
	}); err != nil {
		t.Fatal(err)
	}
	cfg.SetupReady = true
	cfg.Web.InitReady = true
	return runtime, runtime.Handler(), path, func() {
		t.Helper()
		// 只模拟旧向导已经完成的配置提交，不连接真实 DNS 或证书签发服务。
		if err := cfg.Save(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDesktopSameHandlerActivatesSavedSetupAndAllowsLogin(t *testing.T) {
	runtime, handler, _, save := prepareDesktopTransition(t)
	if runtime.core != nil {
		t.Fatal("未保存配置前不应启动正式服务")
	}
	save()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/auth/login", strings.NewReader(`{"identifier":"test-admin","password":"Test1234!"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatalf("保存后原处理器不能登录：%d", response.Code)
	}
	if runtime.core == nil || runtime.initial != nil || runtime.initialDNS != nil {
		t.Fatal("初始化运行时没有切换为正式服务")
	}
	if len(response.Result().Cookies()) == 0 {
		t.Fatal("登录没有返回会话")
	}
	cookie := response.Result().Cookies()[0]
	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("正式登录会话不可用")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/setup", nil))
	if response.Code != http.StatusNotFound {
		t.Fatal("切换后旧初始化写接口仍可访问")
	}
}

func TestDesktopConcurrentTransitionAndIdempotentClose(t *testing.T) {
	runtime, handler, _, save := prepareDesktopTransition(t)
	save()
	var pending sync.WaitGroup
	for index := 0; index < 16; index++ {
		pending.Go(func() {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil))
			if response.Code != http.StatusUnauthorized {
				t.Errorf("并发切换后的路由状态错误：%d", response.Code)
			}
		})
	}
	pending.Wait()
	for index := 0; index < 8; index++ {
		pending.Go(func() {
			if err := runtime.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	pending.Wait()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("关闭后不应重新创建正式服务")
	}
}

func TestDesktopTransitionRejectsCorruptedSavedConfig(t *testing.T) {
	runtime, handler, path, _ := prepareDesktopTransition(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if response.Code != http.StatusServiceUnavailable || runtime.core != nil {
		t.Fatal("损坏配置不得回退到新的初始化或启动正式服务")
	}
}
