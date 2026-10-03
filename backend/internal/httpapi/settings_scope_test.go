package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestSettingsReadScope(t *testing.T) {
	f, secrets := newDNSAccountScopeFixture(t)
	f.server.config.Notification.SMTPUsername = "system-only-notification-account"
	for _, item := range []struct {
		actor string
		want  int
	}{
		{"", http.StatusUnauthorized},
		{"global-admin", http.StatusForbidden},
		{"manager", http.StatusForbidden},
		{"viewer", http.StatusForbidden},
		{"target", http.StatusForbidden},
		{"primary-super-admin", http.StatusOK},
	} {
		t.Run(item.actor, func(t *testing.T) {
			response := serveUserRequest(f.server, f.cookies[item.actor], http.MethodGet, "/api/v1/settings", "")
			if response.Code != item.want {
				t.Fatalf("系统设置读取范围错误：%s，状态=%d", item.actor, response.Code)
			}
			if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
				t.Error("系统设置响应缺少身份缓存隔离")
			}
			if item.want != http.StatusOK {
				for _, forbidden := range []string{"project-a", "system-only-notification-account", "account_email", "credential_configured"} {
					if strings.Contains(response.Body.String(), forbidden) {
						t.Fatal("被拒绝的请求泄露系统元数据")
					}
				}
			} else if !strings.Contains(response.Body.String(), "system-only-notification-account") {
				t.Fatal("超级管理员读取丢失设置内容")
			}
			if secrets.getCalls != 0 {
				t.Fatal("读取设置不应读取密钥明文")
			}
		})
	}
}

func TestSettingsReadRejectsChangedAccountSession(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	cookie := httpUserSessionCookie(f.server, "temporary-super-admin", repository.UserRoleSuperAdmin)
	response := serveUserRequest(f.server, cookie, http.MethodGet, "/api/v1/settings", "")
	if response.Code != http.StatusOK {
		t.Fatal("初始超级管理员不可读")
	}
	// 修改隔离数据库的角色，原 cookie 不得继续继承曾经的系统读取权限。
	if _, err := f.db.ExecContext(t.Context(), "UPDATE users SET role = ? WHERE id = ?", repository.UserRoleAdmin, "temporary-super-admin"); err != nil {
		t.Fatal(err)
	}
	response = serveUserRequest(f.server, cookie, http.MethodGet, "/api/v1/settings", "")
	if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("降权后的旧会话未失效或可被缓存：%d", response.Code)
	}
}
