package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestManagedDNSAccountCatalogForProjectUser(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for _, actor := range []string{"", "manager", "viewer", "global-admin"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dns/providers", nil)
		if actor != "" {
			req.AddCookie(f.cookies[actor])
		}
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, req)
		status := http.StatusOK
		if actor == "" {
			status = http.StatusUnauthorized
		}
		if response.Code != status || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("服务商目录认证或缓存隔离错误：%s %d", actor, response.Code)
		}
	}
}

func TestManagedDNSAccountProjectCapability(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	if err := f.members.GrantProjectRole(t.Context(), "primary-super-admin", "project-a", "target", domain.ProjectRoleDNSAdmin); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"primary-super-admin", "manager", "viewer", "target", "global-admin"} {
		page, err := f.members.ListVisibleProjects(t.Context(), actor, "", 30)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			expected := actor == "primary-super-admin" || actor == "manager" || actor == "target"
			if item.CanManageDNS != expected {
				t.Fatalf("DNS 管理提示与项目角色不符：%s %+v", actor, item)
			}
		}
	}
	if _, err := f.db.ExecContext(t.Context(), "DELETE FROM project_members WHERE user_id = 'target'"); err != nil {
		t.Fatal(err)
	}
	response := serveUserRequest(f.server, f.cookies["target"], http.MethodGet, "/api/v1/projects", "")
	var page repository.VisibleProjectPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || len(page.Items) != 0 {
		t.Fatal("撤权后的旧会话仍有项目管理提示", err)
	}
}
