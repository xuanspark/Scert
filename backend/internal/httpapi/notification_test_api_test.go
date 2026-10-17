package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestNotificationTestEndpointRequiresSuperAdmin(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	body := `{"recipient":"test@example.com","smtp_enabled":true}`

	// 未登录
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notification/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized, got %d", w.Code)
	}

	// 普通管理员（无系统设置权限）
	adminCookie := httpUserSessionCookie(f.server, "admin-user", repository.UserRoleAdmin)
	adminResp := serveUserRequest(f.server, adminCookie, http.MethodPost, "/api/v1/notification/test", body)
	if adminResp.Code != http.StatusForbidden {
		t.Fatalf("expected 403 forbidden for admin, got %d", adminResp.Code)
	}

	// 超级管理员，缺少收件人参数
	superCookie := httpUserSessionCookie(f.server, "super-user", repository.UserRoleSuperAdmin)
	invalidResp := serveUserRequest(f.server, superCookie, http.MethodPost, "/api/v1/notification/test", `{}`)
	if invalidResp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 bad request for empty recipient, got %d", invalidResp.Code)
	}
}
