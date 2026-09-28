package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestProjectLifecycleAPIFlow(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	owner := f.cookies["primary-super-admin"]
	created := serveUserRequest(f.server, owner, http.MethodPost, "/api/v1/projects", `{"name":"新项目","description":"项目说明"}`)
	var item domain.Project
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &item) != nil || item.ID == "" || item.Name != "新项目" {
		t.Fatalf("项目创建失败：%d，%s", created.Code, created.Body.String())
	}
	path := "/api/v1/projects/" + item.ID
	updated := serveUserRequest(f.server, owner, http.MethodPut, path, `{"name":"更新项目","description":"更新说明"}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("项目更新失败：%d，%s", updated.Code, updated.Body.String())
	}
	stored, err := repository.NewSQLProjectRepository(f.db).GetProject(t.Context(), item.ID)
	if err != nil || stored.Name != "更新项目" || !stored.CreatedAt.Equal(item.CreatedAt) {
		t.Fatalf("项目资料或创建时间错误：%+v，%v", stored, err)
	}
	deleted := serveUserRequest(f.server, owner, http.MethodDelete, path, fmt.Sprintf(`{"confirmation":%q}`, item.ID))
	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("项目删除失败：%d，%s", deleted.Code, deleted.Body.String())
	}
	for _, response := range []string{created.Header().Get("Cache-Control"), updated.Header().Get("Cache-Control"), deleted.Header().Get("Cache-Control")} {
		if !strings.Contains(response, "no-store") {
			t.Fatal("项目变更响应允许缓存")
		}
	}
	log := f.lastAudit(t)
	if log.Action != "project.delete" || log.Status != "success" || log.ProjectID != item.ID || log.OperatorID != "primary-super-admin" {
		t.Fatalf("删除后的审计快照错误：%+v", log)
	}
}

func TestProjectLifecycleAPIAuthorization(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for _, tc := range []struct {
		name, actor, method, project string
		status                       int
	}{
		{"anonymous", "", http.MethodPost, "", 401},
		{"global_admin_create", "global-admin", http.MethodPost, "", 403},
		{"project_admin_create", "manager", http.MethodPost, "", 403},
		{"global_admin_update", "global-admin", http.MethodPut, "project-a", 403},
		{"viewer_update", "viewer", http.MethodPut, "project-a", 403},
		{"cross_project", "manager", http.MethodPut, "project-b", 403},
		{"missing", "primary-super-admin", http.MethodPut, "missing", 403},
		{"manager_update", "manager", http.MethodPut, "project-a", 200},
		{"default_protected", "primary-super-admin", http.MethodDelete, "default", 409},
		{"member_reference", "primary-super-admin", http.MethodDelete, "project-a", 409},
		{"manager_reference", "manager", http.MethodDelete, "project-a", 409},
		{"cross_project_delete", "manager", http.MethodDelete, "project-b", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, body := "/api/v1/projects", `{"name":"新名称","description":"不能进入审计的说明"}`
			if tc.project != "" {
				path += "/" + tc.project
			}
			if tc.method == http.MethodDelete {
				body = fmt.Sprintf(`{"confirmation":%q}`, tc.project)
			}
			response := serveUserRequest(f.server, f.cookies[tc.actor], tc.method, path, body)
			if response.Code != tc.status {
				t.Fatalf("项目权限结果错误：%d，%s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("错误响应允许缓存")
			}
			if tc.status >= 400 {
				log := f.lastAudit(t)
				if log.OperatorID != tc.actor || log.Status != "failed" || !strings.Contains(log.Message, response.Header().Get(requestIDHeader)) || strings.Contains(log.Message, "不能进入审计") {
					t.Fatalf("失败审计身份、关联或脱敏错误：%+v", log)
				}
				if (tc.project == "missing" || tc.project == "") && (log.ScopeType != repository.ScopeUnknown || log.ProjectID != "") {
					t.Fatalf("伪造项目归属：%+v", log)
				}
			}
		})
	}
}

func TestProjectLifecycleAPIRejectsInvalidInput(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for _, tc := range []struct{ method, suffix, body string }{
		{http.MethodPost, "", `{"name":"名称","id":"forged"}`},
		{http.MethodPost, "", `{"name":"名称","actor_id":"forged"}`},
		{http.MethodPost, "", `{"name":"名称","created_at":"2026-01-01"}`},
		{http.MethodPost, "", `{"name":"名称"} {}`},
		{http.MethodPost, "", `null`},
		{http.MethodPost, "", `{"name":"   "}`},
		{http.MethodPost, "", `{"name":"` + strings.Repeat("a", 129) + `"}`},
		{http.MethodPut, "/project-b", `{"name":"名称","owner_id":"forged"}`},
		{http.MethodPut, "/project-b?ignored=1", `{"name":"名称"}`},
		{http.MethodPut, "/project-b", strings.Repeat("x", 70*1024)},
		{http.MethodDelete, "/project-b", `{}`},
		{http.MethodDelete, "/project-b", `{"confirmation":"project-a"}`},
		{http.MethodDelete, "/project-b", `{"confirmation":"project-b","actor_id":"forged"}`},
		{http.MethodDelete, "/project-b?ignored=1", `{"confirmation":"project-b"}`},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.method, len(tc.body))+tc.suffix, func(t *testing.T) {
			response := serveUserRequest(f.server, f.cookies["primary-super-admin"], tc.method, "/api/v1/projects"+tc.suffix, tc.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("非法输入未拒绝：%d，%s", response.Code, response.Body.String())
			}
			if log := f.lastAudit(t); log.Status != "failed" || strings.Contains(log.Message, "forged") {
				t.Fatalf("失败审计错误：%+v", log)
			}
		})
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 3 {
		t.Fatalf("非法请求改变项目：%d，%v", count, err)
	}
}

func TestProjectLifecycleAPIAuditFailureRollsBack(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, mode := range []string{"ABORT, 'sensitive-marker'", "IGNORE"} {
			t.Run(method+mode, func(t *testing.T) {
				f := newProjectMemberAPIFixture(t)
				_, err := f.db.ExecContext(t.Context(), `CREATE TRIGGER reject_project_audit BEFORE INSERT ON audit_logs
					WHEN NEW.resource_type='project' AND NEW.status='success' BEGIN SELECT RAISE(`+mode+`); END`)
				if err != nil {
					t.Fatal(err)
				}
				path, body := "/api/v1/projects", `{"name":"不能保存","description":"sensitive-marker"}`
				if method != http.MethodPost {
					path += "/project-b"
				}
				if method == http.MethodDelete {
					body = `{"confirmation":"project-b"}`
				}
				response := serveUserRequest(f.server, f.cookies["primary-super-admin"], method, path, body)
				if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "sensitive-marker") {
					t.Fatalf("审计失败误报或泄密：%d，%s", response.Code, response.Body.String())
				}
				var count int
				if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 3 {
					t.Fatalf("项目数量未回滚：%d，%v", count, err)
				}
				var name string
				if err := f.db.QueryRowContext(t.Context(), "SELECT name FROM projects WHERE id='project-b'").Scan(&name); err != nil || name != "project-b" {
					t.Fatalf("项目内容未回滚：%s，%v", name, err)
				}
				if log := f.lastAudit(t); log.Status != "failed" || log.OperatorID != "primary-super-admin" || strings.Contains(log.Message, "sensitive-marker") {
					t.Fatalf("失败审计未保存或泄密：%+v", log)
				}
			})
		}
	}
}
