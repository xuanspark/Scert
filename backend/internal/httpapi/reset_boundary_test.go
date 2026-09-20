package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
)

// newLegacyResetServer 只保留旧处理函数的独立回归，不代表正式产品路由或生产验收。
func newLegacyResetServer(dependencies Dependencies) *Server {
	server := New(dependencies)
	server.router.GET(admin.ResetPath, server.resetPage)
	server.router.POST(admin.ResetPath, server.resetSubmit)
	return server
}

func TestProductionResetPathClosedEvenWithLegacyActivation(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Web.Hostname = "scert.example.test"
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html>scert-test-shell</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	controller := admin.New(cfg)
	if _, err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Stop() })
	server := New(Dependencies{Config: cfg, FrontendDir: staticDir, Repository: &stubDomainRepository{}, ResetStatePath: admin.StatePath(cfg.Paths.Data)})
	for _, route := range server.router.Routes() {
		if route.Path == "/reset" || strings.HasPrefix(route.Path, "/reset/") {
			t.Fatal("正式路由注册了旧密码重置处理器")
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
		for _, path := range []string{"/reset", "/reset/", "/reset/anything", "/%72eset"} {
			for _, authenticated := range []bool{false, true} {
				request := httptest.NewRequest(method, "http://scert.example.test"+path, strings.NewReader("reset_code=test&new_password=TestPass123"))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				if authenticated {
					serveAdminHTTP(server, response, request)
				} else {
					server.Handler().ServeHTTP(response, request)
				}
				if response.Code != http.StatusNotFound {
					t.Fatalf("正式重置路径必须关闭：%s %s authenticated=%v status=%d", method, path, authenticated, response.Code)
				}
				if strings.Contains(response.Body.String(), "scert-test-shell") || len(response.Result().Cookies()) != 0 {
					t.Fatal("旧重置路径回退到页面或写入了重置 Cookie")
				}
			}
		}
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://scert.example.test/login", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "scert-test-shell") {
		t.Fatal("边界测试没有使用启用的真实页面回退处理器")
	}
}
