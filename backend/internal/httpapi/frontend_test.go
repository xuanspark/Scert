package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestFrontendServesAssetsAndHistoryRoutes(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html>Scert</html>"), 0o644); err != nil {
		t.Fatalf("写入前端入口失败：%v", err)
	}
	if err := os.Mkdir(filepath.Join(staticDir, "assets"), 0o755); err != nil {
		t.Fatalf("创建静态资源目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "assets", "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("写入静态资源失败：%v", err)
	}

	cfg := config.Default(t.TempDir())
	server := New(Dependencies{Config: cfg, FrontendDir: staticDir})

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root", method: http.MethodGet, path: "/", wantStatus: http.StatusOK, wantBody: "Scert"},
		{name: "history route", method: http.MethodGet, path: "/login", wantStatus: http.StatusOK, wantBody: "Scert"},
		{name: "asset", method: http.MethodGet, path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "console.log"},
		{name: "missing asset", method: http.MethodGet, path: "/assets/missing.js", wantStatus: http.StatusNotFound},
		{name: "unknown api", method: http.MethodGet, path: "/api/unknown", wantStatus: http.StatusNotFound},
		{name: "post fallback", method: http.MethodPost, path: "/login", wantStatus: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d，响应：%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantBody != "" && !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("响应未包含 %q：%s", test.wantBody, response.Body.String())
			}
		})
	}
}

func TestFrontendRespectsWebEnabled(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("Scert"), 0o644); err != nil {
		t.Fatalf("写入前端入口失败：%v", err)
	}
	cfg := config.Default(t.TempDir())
	cfg.Web.Enabled = false
	server := New(Dependencies{Config: cfg, FrontendDir: staticDir})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("关闭 Web 后根路径应返回 404：%d", response.Code)
	}
}
