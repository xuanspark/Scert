package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicPasswordResetGuidanceDoesNotEnumerateAccounts(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "owner"
	cfg.Admin.Email = "owner@example.test"
	server := New(Dependencies{Config: cfg})
	var baseline string
	for _, identifier := range []string{"owner", "owner@example.test", "OWNER", "OWNER@EXAMPLE.TEST", "not-an-account", "absent@example.test", " owner "} {
		t.Run(identifier, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"identifier": identifier})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/password-reset/resolve", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("公开查询应返回通用成功响应：%d %s", response.Code, response.Body.String())
			}
			if baseline == "" {
				baseline = response.Body.String()
			} else if response.Body.String() != baseline {
				t.Fatalf("不同合法标识不得产生可区分的响应：%s", response.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 3 || payload["action"] != "contact_administrator" || payload["title"] != "密码重置" || payload["description"] != "当前方式无法使用，请联系运维人员辅助解决。" {
				t.Fatalf("公开响应必须仅包含通用处理提示：%v", payload)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("密码处理提示不得被缓存")
			}
			if len(response.Result().Cookies()) != 0 || len(server.authSessions) != 0 || len(server.loginPending) != 0 {
				t.Fatal("公开查询不得创建会话或登录状态")
			}
		})
	}
}

func TestPublicPasswordResetGuidancePreservesInputBoundary(t *testing.T) {
	for _, ready := range []bool{false, true} {
		cfg := readyTestConfig(t.TempDir())
		cfg.SetupReady = ready
		server := New(Dependencies{Config: cfg})
		for _, body := range []string{`{}`, `{"identifier":""}`, `{"identifier":17}`, `{"identifier":"owner","unexpected":true}`, `{"identifier":"owner"}{"identifier":"owner"}`, `{"identifier":"` + strings.Repeat("a", passwordResetResolveBodyLimit) + `"}`} {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/password-reset/resolve", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("无效请求必须被拒绝：ready=%t status=%d", ready, response.Code)
			}
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/password-reset/resolve", strings.NewReader(`{"identifier":"owner"}`))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		want := http.StatusOK
		if !ready {
			want = http.StatusConflict
		}
		if response.Code != want {
			t.Fatalf("初始化边界不正确：ready=%t status=%d", ready, response.Code)
		}
	}
}
