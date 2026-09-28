package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestProjectBindingAPIPermissionsAndExplicitClear(t *testing.T) {
	f, _ := newProjectResourceAPIFixture(t)
	path := "/api/v1/domains/domain-a/servers"
	for _, tc := range []struct {
		actor string
		code  int
	}{
		{"manager", 200}, {"primary-super-admin", 200}, {"viewer", 403}, {"global-admin", 403}, {"target", 403},
	} {
		response := serveUserRequest(f.server, f.cookies[tc.actor], http.MethodPut, path, `{"server_ids":["server-a","server-a"]}`)
		if response.Code != tc.code || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("绑定权限或缓存错误：%s，%d，%s", tc.actor, response.Code, response.Body.String())
		}
		entry := f.lastAudit(t)
		if entry.Action != "domain.servers.replace" || entry.ProjectID != "project-a" || entry.OperatorID != tc.actor || strings.Contains(entry.Message, "server_ids") {
			t.Fatalf("绑定审计不正确或包含请求体：%+v", entry)
		}
	}
	response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPut, path, `{"server_ids":[]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("清空响应错误：%d，%s", response.Code, response.Body.String())
	}
	items, err := f.server.servers.ListServersByDomainID(t.Context(), "domain-a")
	if err != nil || len(items) != 0 {
		t.Fatalf("绑定未清空：%+v，%v", items, err)
	}
}

func TestProjectBindingAPIRejectsForgeryAndKeepsOldBindings(t *testing.T) {
	f, _ := newProjectResourceAPIFixture(t)
	for _, tc := range []struct {
		id, query, body string
		code            int
	}{
		{"domain-a", "", `{}`, 400},
		{"domain-a", "", `{"server_ids":null}`, 400},
		{"domain-a", "", `{"server_ids":[],"project_id":"project-b"}`, 400},
		{"domain-a", "?actor_id=primary-super-admin", `{"server_ids":[]}`, 400},
		{"domain-a", "", `{"server_ids":["../server"]}`, 400},
		{"domain-a", "", `{"server_ids":["server-b"]}`, 403},
		{"domain-a", "", `{"server_ids":["missing"]}`, 403},
		{"domain-b", "", `{"server_ids":[]}`, 403},
		{"missing", "", `{"server_ids":[]}`, 403},
	} {
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPut, "/api/v1/domains/"+tc.id+"/servers"+tc.query, tc.body)
		if response.Code != tc.code {
			t.Fatalf("无效或越权请求未拒绝：%s，%d，%s", tc.id, response.Code, response.Body.String())
		}
		items, err := f.server.servers.ListServersByDomainID(t.Context(), "domain-a")
		if err != nil || len(items) != 1 || items[0].ID != "server-a" {
			t.Fatalf("拒绝请求改变了原绑定：%+v，%v", items, err)
		}
		entry := f.lastAudit(t)
		wantProject := "project-a"
		if tc.id == "domain-b" {
			wantProject = "project-b"
		} else if tc.id == "missing" {
			wantProject = ""
		}
		if entry.ProjectID != wantProject || entry.OperatorID != "manager" || entry.Status != "failed" {
			t.Fatalf("失败审计未按真实资源归属记录：%+v", entry)
		}
	}
}

type bindingWriteBoundary struct {
	repository.ProjectBindingRepository
	before func()
}

func (b bindingWriteBoundary) ReplaceProjectDomainServers(ctx context.Context, actor, id string, ids []string) ([]deployment.Server, error) {
	b.before()
	return b.ProjectBindingRepository.ReplaceProjectDomainServers(ctx, actor, id, ids)
}

func TestProjectBindingAPIRechecksAfterSessionAndFailsClosed(t *testing.T) {
	for _, mode := range []string{"revoke", "disable", "missing_dependency"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newProjectResourceAPIFixture(t)
			f.server.projectBindings = bindingWriteBoundary{ProjectBindingRepository: f.members, before: func() {
				if mode == "revoke" {
					if err := f.members.RevokeProjectRole(t.Context(), "primary-super-admin", "project-a", "manager", domain.ProjectRoleAdmin); err != nil {
						t.Fatal(err)
					}
				} else if _, err := f.db.ExecContext(t.Context(), `UPDATE users SET status='disabled' WHERE id='manager'`); err != nil {
					t.Fatal(err)
				}
			}}
			want := http.StatusForbidden
			if mode == "missing_dependency" {
				f.server.projectBindings = nil
				want = http.StatusServiceUnavailable
			}
			response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPut, "/api/v1/domains/domain-a/servers", `{"server_ids":[]}`)
			if response.Code != want {
				t.Fatalf("会话后撤权或缺少依赖未拒绝：%d，%s", response.Code, response.Body.String())
			}
			items, err := f.server.servers.ListServersByDomainID(t.Context(), "domain-a")
			if err != nil || len(items) != 1 {
				t.Fatalf("失败后丢失原绑定：%+v，%v", items, err)
			}
		})
	}
}

func TestProjectBindingAPIAuthenticationAndOrigin(t *testing.T) {
	f, _ := newProjectResourceAPIFixture(t)
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/domains/domain-a/servers", strings.NewReader(`{"server_ids":[]}`))
		request.Header.Set("Content-Type", "application/json")
		want := http.StatusUnauthorized
		if authenticated {
			request.AddCookie(f.cookies["manager"])
			request.Header.Set("Origin", "https://foreign.example.test")
			want = http.StatusForbidden
		}
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		if response.Code != want || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("未登录或跨来源未拒绝：%d，%s", response.Code, response.Body.String())
		}
		if entry := f.lastAudit(t); entry.Status != "failed" || entry.Action != "domain.servers.replace" {
			t.Fatalf("认证失败缺少审计：%+v", entry)
		}
	}
}
