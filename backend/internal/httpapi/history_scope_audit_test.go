package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestHistoryScopeLoginAuditUsesVerifiedAccount(t *testing.T) {
	server, _, audit, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	server.config.Admin.Username = "owner"
	server.config.Admin.PasswordHash = httpUserPasswordHash(t, "OwnerPass123")
	otherCookie := httpUserSessionCookie(server, "other-actor", repository.UserRoleAdmin)
	for _, tc := range []struct {
		name, password, actor string
		cookie                *http.Cookie
		status                int
	}{
		{"fresh-login", "OwnerPass123", "primary-super-admin", nil, http.StatusOK},
		{"different-session", "OwnerPass123", "primary-super-admin", otherCookie, http.StatusOK},
		{"failed-login", "Incorrect123", "", otherCookie, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"owner","password":"`+tc.password+`"}`))
			request.Header.Set("Content-Type", "application/json")
			if tc.cookie != nil {
				request.AddCookie(tc.cookie)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("登录响应错误：%d，%s", response.Code, response.Body.String())
			}
			logs, err := audit.ListAuditLogs(t.Context(), 1)
			if err != nil || len(logs) != 1 {
				t.Fatalf("登录审计缺失：%+v，%v", logs, err)
			}
			if logs[0].OperatorID != tc.actor || logs[0].ScopeType != repository.ScopeSystem || logs[0].ProjectID != "" {
				t.Fatalf("登录审计误用旧会话或丢失已验证身份：%+v", logs[0])
			}
		})
	}
}

func TestHistoryScopeUserAuditSurvivesSessionInvalidation(t *testing.T) {
	server, _, audit, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	cookie := httpUserSessionCookie(server, "self-admin", repository.UserRoleSuperAdmin)
	response := serveUserRequest(server, cookie, http.MethodPut, "/api/v1/users/self-admin",
		`{"username":"self-admin","email":"self-admin@example.test","password":"Updated123","role":"super_admin","status":"active"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("更新自身账户失败：%d，%s", response.Code, response.Body.String())
	}
	logs, err := audit.ListAuditLogs(t.Context(), 1)
	if err != nil || len(logs) != 1 || logs[0].OperatorID != "self-admin" || logs[0].ScopeType != repository.ScopeSystem {
		t.Fatalf("会话失效后丢失实际操作者：%+v，%v", logs, err)
	}
	after := serveUserRequest(server, cookie, http.MethodGet, "/api/v1/users", "")
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("审计身份快照不得恢复失效会话：%d", after.Code)
	}
}

func TestHistoryScopePasswordAuditSurvivesSessionInvalidation(t *testing.T) {
	server, _, audit, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	cookie := httpUserSessionCookie(server, "password-user", repository.UserRoleUser)
	response := performChangePasswordRequest(t, server, cookie,
		`{"current_password":"FixturePass123","new_password":"Updated123","confirm_password":"Updated123"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("修改密码失败：%d，%s", response.Code, response.Body.String())
	}
	logs, err := audit.ListAuditLogs(t.Context(), 1)
	if err != nil || len(logs) != 1 || logs[0].OperatorID != "password-user" || logs[0].ScopeType != repository.ScopeSystem {
		t.Fatalf("改密后丢失审计操作者：%+v，%v", logs, err)
	}
	if after := authSessionRequest(server, cookie); after.Code != http.StatusUnauthorized {
		t.Fatalf("改密审计不得恢复旧会话：%d", after.Code)
	}
}
