package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestRequestMiddlewareAddsAndPreservesRequestID(t *testing.T) {
	server := New(Dependencies{Config: config.Default(t.TempDir()), Repository: &stubDomainRepository{}})

	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	generated := first.Header().Get(requestIDHeader)
	if !validRequestID(generated) {
		t.Fatalf("请求 ID 未正确生成：%q", generated)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	secondRequest.Header.Set(requestIDHeader, "client-request-123")
	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, secondRequest)
	if actual := second.Header().Get(requestIDHeader); actual != "client-request-123" {
		t.Fatalf("请求 ID 未保留：%q", actual)
	}
}

func TestAdminAPIRequiresAuthenticationAfterSetup(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(hash)

	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问管理接口应返回 401，实际为 %d：%s", response.Code, response.Body.String())
	}
}

func TestAdminAPIAcceptsAuthenticatedSessionAndRejectsCrossOrigin(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(hash)
	repository := &stubDomainRepository{items: []domain.Domain{}}
	server := New(Dependencies{Config: cfg, Repository: repository})

	loginBody := strings.NewReader(`{"identifier":"admin","password":"Password123"}`)
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("登录失败：%d %s", loginResponse.Code, loginResponse.Body.String())
	}
	cookie := loginResponse.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("登录响应未返回会话 Cookie")
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil)
	authenticatedRequest.AddCookie(cookie[0])
	authenticatedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(authenticatedResponse, authenticatedRequest)
	if authenticatedResponse.Code != http.StatusOK {
		t.Fatalf("登录后访问管理接口失败：%d %s", authenticatedResponse.Code, authenticatedResponse.Body.String())
	}

	crossOriginRequest := httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil)
	crossOriginRequest.AddCookie(cookie[0])
	crossOriginRequest.Host = "127.0.0.1:5200"
	crossOriginRequest.Header.Set("Origin", "https://malicious.example")
	crossOriginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossOriginResponse, crossOriginRequest)
	if crossOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("跨来源管理请求应返回 403，实际为 %d：%s", crossOriginResponse.Code, crossOriginResponse.Body.String())
	}
}

func TestAdminAPIRejectsAuthenticatedRegularUser(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(hash)
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	cookie := httpUserSessionCookie(server, "user-1", repository.UserRoleUser)
	response := serveUserRequest(server, cookie, http.MethodGet, "/api/v1/domains", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("普通用户访问管理接口应返回 403，实际为 %d：%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "没有管理权限") {
		t.Fatalf("普通用户权限错误提示不明确：%s", response.Body.String())
	}
}

func TestAdminAPIRequiresSetupBeforeAccessingManagementHandlers(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = false
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	paths := []string{
		"/api/v1/domains",
		"/api/v1/settings",
		"/api/v1/servers",
		"/api/v1/tasks",
		"/api/v1/audit-logs",
		"/api/v1/certificates",
		"/api/v1/ssh-keys",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusConflict {
				t.Fatalf("初始化前访问 %s 应返回 409，实际为 %d：%s", path, response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "请先完成初始化配置") {
				t.Fatalf("初始化前响应缺少中文提示：%s", response.Body.String())
			}
		})
	}
}
