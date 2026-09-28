package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const memberTestPath = "/api/v1/projects/project-a/members"

func TestProjectMemberAPIAuthorizationAndFailureAudit(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for _, tc := range []struct {
		name, actor, path string
		status            int
	}{
		{"anonymous", "", memberTestPath, http.StatusUnauthorized},
		{"global_admin_is_not_project_admin", "global-admin", memberTestPath, http.StatusForbidden},
		{"read_only_cannot_list_members", "viewer", memberTestPath, http.StatusForbidden},
		{"other_project", "manager", "/api/v1/projects/project-b/members", http.StatusForbidden},
		{"missing_project", "primary-super-admin", "/api/v1/projects/missing/members", http.StatusForbidden},
		{"project_admin_is_regular_user", "manager", memberTestPath, http.StatusOK},
		{"super_admin_empty_project", "primary-super-admin", "/api/v1/projects/project-b/members", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := serveUserRequest(f.server, f.cookies[tc.actor], http.MethodGet, tc.path, "")
			if response.Code != tc.status {
				t.Fatalf("成员列表权限错误：%d，%s", response.Code, response.Body.String())
			}
			for _, secret := range []string{"password_hash", "FixturePass123", "email", "groups", "example.test"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("成员列表暴露非必要账户字段：%s", response.Body.String())
				}
			}
			if tc.status >= 400 {
				log := f.lastAudit(t)
				if log.OperatorID != tc.actor || log.Status != "failed" || log.Action != "project_member.list" || !strings.Contains(log.Message, response.Header().Get(requestIDHeader)) {
					t.Fatalf("失败审计身份或关联错误：%+v", log)
				}
				if tc.name == "missing_project" && (log.ScopeType != repository.ScopeUnknown || log.ProjectID != "") {
					t.Fatalf("不存在项目被伪造归属：%+v", log)
				}
			} else {
				var page repository.ProjectMemberPage
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Items == nil {
					t.Fatalf("成员列表不是稳定数组响应：%s，%v", response.Body.String(), err)
				}
			}
		})
	}
	t.Run("cross_origin_identity_is_verified", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, memberTestPath, strings.NewReader(`{"user_id":"target","role":"dns_admin"}`))
		request.AddCookie(f.cookies["manager"])
		request.Header.Set("Origin", "https://untrusted.example.test")
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		log := f.lastAudit(t)
		if response.Code != http.StatusForbidden || log.OperatorID != "manager" || log.Status != "failed" {
			t.Fatalf("来源拒绝丢失已验证身份：%d，%+v", response.Code, log)
		}
	})
}

func TestProjectMemberAPIMutations(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	body := `{"user_id":"target","role":"dns_admin"}`
	for _, actor := range []string{"global-admin", "viewer"} {
		response := serveUserRequest(f.server, f.cookies[actor], http.MethodPost, memberTestPath, body)
		log := f.lastAudit(t)
		if response.Code != http.StatusForbidden || log.OperatorID != actor || log.ResourceID != "target" || log.Status != "failed" {
			t.Fatalf("未授权授予或审计错误：%d，%+v", response.Code, log)
		}
	}
	response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPost, memberTestPath, body)
	log := f.lastAudit(t)
	if response.Code != http.StatusCreated || log.OperatorID != "manager" || log.ProjectID != "project-a" || log.Status != "success" || log.Action != "project_member.grant" {
		t.Fatalf("授予角色或事务审计错误：%d，%+v", response.Code, log)
	}
	duplicate := serveUserRequest(f.server, f.cookies["manager"], http.MethodPost, memberTestPath, body)
	if duplicate.Code != http.StatusConflict || f.lastAudit(t).Status != "failed" {
		t.Fatalf("重复授予未明确拒绝：%d，%s", duplicate.Code, duplicate.Body.String())
	}
	response = serveUserRequest(f.server, f.cookies["manager"], http.MethodDelete, memberTestPath+"/target/roles/dns_admin", "")
	log = f.lastAudit(t)
	if response.Code != http.StatusNoContent || log.Status != "success" || log.Action != "project_member.revoke" || log.OperatorID != "manager" {
		t.Fatalf("撤销角色或审计错误：%d，%+v", response.Code, log)
	}
	response = serveUserRequest(f.server, f.cookies["manager"], http.MethodDelete, memberTestPath+"/manager/roles/project_admin", "")
	if response.Code != http.StatusNoContent || f.lastAudit(t).OperatorID != "manager" {
		t.Fatalf("自我撤权丢失身份：%d", response.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response = serveUserRequest(f.server, f.cookies["manager"], method, memberTestPath, body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("自我撤权后仍可读取或授权：%s，%d", method, response.Code)
		}
	}
}

func TestProjectMemberAPIPageAndInputValidation(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	for i := 0; i < 11; i++ {
		id := fmt.Sprintf("paged-%02d", i)
		httpUserSessionCookie(f.server, id, repository.UserRoleUser)
		for _, role := range []domain.ProjectRole{domain.ProjectRoleDNSAdmin, domain.ProjectRoleReadOnly} {
			if err := f.members.GrantProjectRole(t.Context(), "primary-super-admin", "project-a", id, role); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, size := range []int{5, 10, 15, 30} {
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodGet, fmt.Sprintf("%s?page_size=%d", memberTestPath, size), "")
		var page repository.ProjectMemberPage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != min(size, 13) || page.PageSize != size || page.HasMore != (size < 13) {
			t.Fatalf("成员分页响应错误：%d，%s", response.Code, response.Body.String())
		}
		if page.HasMore {
			next := serveUserRequest(f.server, f.cookies["manager"], http.MethodGet, fmt.Sprintf("%s?page_size=%d&after=%s", memberTestPath, size, page.NextCursor), "")
			var nextPage repository.ProjectMemberPage
			if next.Code != http.StatusOK || json.Unmarshal(next.Body.Bytes(), &nextPage) != nil || len(nextPage.Items) == 0 || nextPage.Items[0].UserID <= page.NextCursor {
				t.Fatalf("成员下一页游标错误：%s", next.Body.String())
			}
		}
	}
	for _, query := range []string{"page_size=0", "page_size=100000", "page_size=-5", "page_size=abc", "page_size=5&page_size=10", "after=a&after=b", "after=%ZZ", "after=x;bad=y", "actor_id=primary-super-admin", "after=" + strings.Repeat("a", 129)} {
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodGet, memberTestPath+"?"+query, "")
		if response.Code != http.StatusBadRequest || f.lastAudit(t).Status != "failed" {
			t.Fatalf("非法分页参数未拒绝：%s，%d", query, response.Code)
		}
	}
	for _, body := range []string{
		`{"user_id":"target","role":"dns_admin","actor_id":"primary-super-admin"}`,
		`{"user_id":"target","role":"super_admin"}`, `{"user_id":"target","role":"dns_admin"} {}`,
		`{"user_id":"sensitive-marker","role":"invalid"}`, strings.Repeat("x", 70*1024),
	} {
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPost, memberTestPath, body)
		log := f.lastAudit(t)
		if response.Code != http.StatusBadRequest || log.OperatorID != "manager" || strings.Contains(log.Message, "sensitive-marker") || strings.Contains(log.Message, "actor_id") || strings.Contains(log.ResourceID, "sensitive-marker") {
			t.Fatalf("非法授权参数未拒绝或审计泄漏正文：%d，%+v", response.Code, log)
		}
	}
}

func TestProjectMemberAPIStaleSessionAndAuditRollback(t *testing.T) {
	t.Run("disabled_session_is_not_attributed", func(t *testing.T) {
		f := newProjectMemberAPIFixture(t)
		if _, err := f.db.ExecContext(t.Context(), "UPDATE users SET status='disabled' WHERE id='manager'"); err != nil {
			t.Fatal(err)
		}
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPost, memberTestPath, `{"user_id":"target","role":"dns_admin"}`)
		if response.Code != http.StatusUnauthorized || f.lastAudit(t).OperatorID != "" {
			t.Fatalf("无效会话仍被记录为已验证身份：%d，%+v", response.Code, f.lastAudit(t))
		}
	})
	t.Run("success_audit_failure_rolls_back_membership", func(t *testing.T) {
		f := newProjectMemberAPIFixture(t)
		if _, err := f.db.ExecContext(t.Context(), `CREATE TRIGGER reject_member_success BEFORE INSERT ON audit_logs
			WHEN NEW.resource_type='project_member' AND NEW.status='success'
			BEGIN SELECT RAISE(ABORT, 'sensitive-marker'); END`); err != nil {
			t.Fatal(err)
		}
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPost, memberTestPath, `{"user_id":"target","role":"dns_admin"}`)
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "sensitive-marker") {
			t.Fatalf("审计失败误报成功或泄露底层错误：%d，%s", response.Code, response.Body.String())
		}
		var count int
		if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM project_members WHERE user_id='target'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("审计失败仍留下成员授权：%d，%v", count, err)
		}
		log := f.lastAudit(t)
		if log.Status != "failed" || log.OperatorID != "manager" || strings.Contains(log.Message, "sensitive-marker") {
			t.Fatalf("事务回滚后失败审计缺失或泄密：%+v", log)
		}
	})
}
