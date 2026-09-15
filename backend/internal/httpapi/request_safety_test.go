package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func TestAuthMutationRejectsCrossOrigin(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	server := New(Dependencies{Config: cfg})
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/logout"} {
		for _, header := range []string{"Origin", "Referer"} {
			t.Run(path+"/"+header, func(t *testing.T) {
				cookie := loginAuthTest(t, server, "admin", "AdminPass123", false).Result().Cookies()[0]
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"identifier":"admin","password":"AdminPass123"}`))
				request.Header.Set("Content-Type", "text/plain")
				request.Header.Set(header, "https://untrusted.example")
				request.AddCookie(cookie)
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusForbidden {
					t.Fatalf("跨源认证操作应被拒绝：%d", response.Code)
				}
				if len(response.Result().Cookies()) != 0 {
					t.Fatal("跨源请求不应设置或清除会话 Cookie")
				}
				if _, exists := server.authSessions[cookie.Value]; !exists {
					t.Fatal("跨源退出不应删除已有会话")
				}
			})
		}
	}
}

func TestAuthMutationAllowsSameOrigin(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	server := New(Dependencies{Config: cfg})
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/logout"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"identifier":"admin","password":"AdminPass123"}`))
		request.Header.Set("Origin", "http://example.com")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("同源认证操作应保持可用：%s %d", path, response.Code)
		}
	}
}

func TestSetupMutationRejectsCrossOriginLoopback(t *testing.T) {
	for _, path := range []string{"/api/v1/setup", "/api/v1/setup/dns/test"} {
		server := New(Dependencies{Config: config.Default(t.TempDir()), SetupDNS: &fakeSetupDNSVerifier{}})
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("Origin", "https://untrusted.example")
		request.Header.Set("Content-Type", "text/plain")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("本机初始化也必须拒绝跨源写入：%s %d", path, response.Code)
		}
	}
}

type delayedDNSVerifier struct {
	fakeSetupDNSVerifier
}

func (f *delayedDNSVerifier) Verify(ctx context.Context, _ setup.DNSVerificationInput) (setup.DNSVerificationResult, error) {
	select {
	case <-time.After(150 * time.Millisecond):
		return setup.DNSVerificationResult{}, nil
	case <-ctx.Done():
		return setup.DNSVerificationResult{}, ctx.Err()
	}
}

func TestDNSLongResponseSurvivesOrdinaryWriteTimeout(t *testing.T) {
	server := New(Dependencies{Config: config.Default(t.TempDir()), SetupDNS: &delayedDNSVerifier{}})
	// 使用真实套接字而非 ResponseRecorder，缩短默认期限以稳定复现写入超时。
	host := httptest.NewUnstartedServer(server.Handler())
	host.Config = newHTTPServer("", server.Handler())
	host.Config.WriteTimeout = 50 * time.Millisecond
	host.Start()
	defer host.Close()
	client := host.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Post(host.URL+"/api/v1/setup/dns/test", "application/json", strings.NewReader(`{"provider":"aliyun","test_domain":"example.test","credentials":{}}`))
	if err != nil {
		t.Fatalf("长操作完成后浏览器必须仍可读取响应：%v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"verified":true`) {
		t.Fatalf("长操作响应不完整：%d %v", response.StatusCode, err)
	}
}

func TestLongOperationCancelsWorkAndAllowsFinalResponse(t *testing.T) {
	server := New(Dependencies{Config: config.Default(t.TempDir())})
	server.router.POST("/test-operation", server.longOperation(50*time.Millisecond), func(c *gin.Context) {
		<-c.Request.Context().Done()
		if !errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "测试工作流已停止"})
	})
	host := httptest.NewUnstartedServer(server.Handler())
	host.Config = newHTTPServer("", server.Handler())
	host.Config.WriteTimeout = 20 * time.Millisecond
	host.Start()
	defer host.Close()
	client := host.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Post(host.URL+"/test-operation", "application/json", nil)
	if err != nil {
		t.Fatalf("取消后应仍能返回明确错误：%v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusGatewayTimeout || !strings.Contains(string(body), "测试工作流已停止") {
		t.Fatalf("超时结果未完整返回：%d %v", response.StatusCode, err)
	}
}

func TestSetupIssuanceDoesNotBlockStatusOrAllowConcurrentSetup(t *testing.T) {
	directory := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	server := New(Dependencies{
		Config: config.Default(directory), ConfigPath: filepath.Join(directory, "config.json"),
		SetupDNS: &fakeSetupDNSVerifier{},
		IssueSetupCertificate: func(ctx context.Context, _ certificate.SetupCertificateRequest) (certificate.SavedResource, error) {
			close(started)
			select {
			case <-release:
				return certificate.SavedResource{}, errors.New("模拟签发失败")
			case <-ctx.Done():
				return certificate.SavedResource{}, ctx.Err()
			}
		},
	})
	payload := `{"admin_username":"admin","admin_email":"admin@example.test","admin_password":"AdminPass123","server_port":8443,"dns_provider":"aliyun","dns_credential_ref":"secret://dns/aliyun/test","dns_verification_token":"test-token","dns_test_domain":"scert.example.test","web_hostname":"scert.example.test","https_mode":"direct"}`
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(payload))
		r.RemoteAddr = "127.0.0.1:12345"
		return r
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(httptest.NewRecorder(), request())
	}()
	defer func() { close(release); <-done }()
	select {
	case <-started:
	case <-done:
		t.Fatal("初始化未到达模拟签发阶段")
	case <-time.After(2 * time.Second):
		t.Fatal("初始化启动超时")
	}
	for _, path := range []string{"/api/v1/status", "/api/v1/setup"} {
		response := httptest.NewRecorder()
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			r := request()
			if path != "/api/v1/setup" {
				r = httptest.NewRequest(http.MethodGet, path, nil)
			}
			server.Handler().ServeHTTP(response, r)
		}()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatalf("签发期间请求被全局配置锁阻塞：%s", path)
		}
		want := http.StatusOK
		if path == "/api/v1/setup" {
			want = http.StatusConflict
		}
		if response.Code != want {
			t.Fatalf("签发期间响应不正确：%s %d", path, response.Code)
		}
	}
}
