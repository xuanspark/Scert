package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasswordResetResolutionOnlyOffersAdministratorContact(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "owner"
	cfg.Admin.Email = "owner@example.test"
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	for _, identifier := range []string{"owner", "owner@example.test"} {
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
				t.Fatalf("查询失败：%d %s", response.Code, response.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["account_type"] != "super_admin" || payload["action"] != "contact_administrator" {
				t.Fatalf("超级管理员没有返回受控本机处理标识：%v", payload)
			}
			if description := strings.ToLower(response.Body.String()); strings.Contains(description, "scert-admin") || strings.Contains(description, "systemctl") || strings.Contains(description, "local_admin_command") {
				t.Fatalf("公开查询不应泄露本机管理命令：%s", description)
			}
			for _, field := range []string{"reset_path", "reset_url", "token", "nonce"} {
				if _, exists := payload[field]; exists {
					t.Fatalf("公开查询泄露了重置入口或会话字段：%s", field)
				}
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("公开查询不应创建重置会话")
			}
		})
	}
}
