package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestReadyServerDoesNotRegisterLegacySetupWrites(t *testing.T) {
	server := New(Dependencies{Config: readyTestConfig(t.TempDir())})
	for _, item := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/v1/setup/status", http.StatusOK},
		{http.MethodGet, "/api/v1/setup/dns/providers", http.StatusNotFound},
		{http.MethodPost, "/api/v1/setup/dns/test", http.StatusNotFound},
		{http.MethodPost, "/api/v1/setup", http.StatusNotFound},
		{http.MethodGet, "/api/v1/bootstrap/status", http.StatusNotFound},
	} {
		for _, remote := range []string{"127.0.0.1:12345", "203.0.113.20:12345"} {
			request := httptest.NewRequest(item.method, item.path, strings.NewReader(`{}`))
			request.RemoteAddr = remote
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != item.status {
				t.Fatalf("正式服务路由状态错误：%s %s %d", item.method, item.path, response.Code)
			}
		}
	}
}

func TestUninitializedServerCannotStartPlainHTTP(t *testing.T) {
	server := New(Dependencies{Config: config.Default(t.TempDir())})
	if err := server.ListenAndServe(); err == nil || !strings.Contains(err.Error(), "独立 HTTPS") {
		t.Fatalf("未初始化服务应拒绝启动明文监听：%v", err)
	}
	if server.httpServer != nil {
		t.Fatal("未初始化不应创建正式监听服务")
	}
}

func TestShutdownBeforeStartPreventsLaterListener(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	// 停止状态必须在加载证书和监听端口前生效，夹具不需要可用的证书。
	cfg.Server.TLSCert = filepath.Join(dir, "cert.pem")
	cfg.Server.TLSKey = filepath.Join(dir, "key.pem")
	for _, path := range []string{cfg.Server.TLSCert, cfg.Server.TLSKey} {
		if err := os.WriteFile(path, []byte("test-only"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server := New(Dependencies{Config: cfg})
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pending sync.WaitGroup
	for index := 0; index < 16; index++ {
		pending.Go(func() {
			if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("关闭后不应重新监听：%v", err)
			}
		})
		pending.Go(func() {
			if err := server.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	pending.Wait()
}
