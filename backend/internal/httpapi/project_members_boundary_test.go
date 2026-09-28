package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 在会话验证之后、真实仓储执行之前改变权限，避免仅验证顺序请求的撤权场景。
type projectMemberBoundaryRepository struct {
	repository.ProjectMemberRepository
	before func()
	audit  func(context.Context)
}

func (r projectMemberBoundaryRepository) ListProjectMembers(ctx context.Context, actor, project, after string, size int) (repository.ProjectMemberPage, error) {
	r.before()
	return r.ProjectMemberRepository.ListProjectMembers(ctx, actor, project, after, size)
}

func (r projectMemberBoundaryRepository) GrantProjectRole(ctx context.Context, actor, project, user string, role domain.ProjectRole) error {
	r.before()
	return r.ProjectMemberRepository.GrantProjectRole(ctx, actor, project, user, role)
}

func (r projectMemberBoundaryRepository) RevokeProjectRole(ctx context.Context, actor, project, user string, role domain.ProjectRole) error {
	r.before()
	return r.ProjectMemberRepository.RevokeProjectRole(ctx, actor, project, user, role)
}

func (r projectMemberBoundaryRepository) RecordProjectMemberFailure(ctx context.Context, failure repository.ProjectMemberFailure) error {
	if r.audit != nil {
		r.audit(ctx)
	}
	return r.ProjectMemberRepository.RecordProjectMemberFailure(ctx, failure)
}

func TestProjectMemberAPIDeleteRejectsPayload(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	path := memberTestPath + "/manager/roles/project_admin"
	for _, tc := range []struct{ name, suffix, body string }{
		{"json", "", `{"actor_id":"primary-super-admin"}`},
		{"whitespace", "", " "},
		{"large_body", "", strings.Repeat("x", 70*1024)},
		{"query", "?actor_id=primary-super-admin", ""},
		{"unknown_query", "?ignored=1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := serveUserRequest(f.server, f.cookies["manager"], http.MethodDelete, path+tc.suffix, tc.body)
			log := f.lastAudit(t)
			if response.Code != http.StatusBadRequest || log.Status != "failed" || log.OperatorID != "manager" {
				t.Fatalf("撤销角色未拒绝额外输入或审计身份错误：%d，%+v", response.Code, log)
			}
			if _, err := f.members.ListProjectMembers(t.Context(), "manager", "project-a", "", 5); err != nil {
				t.Fatalf("拒绝的请求仍撤销了角色：%v", err)
			}
		})
	}
}

func TestProjectMemberAPIRechecksAfterAuthentication(t *testing.T) {
	for _, change := range []string{"revoke", "disable"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			t.Run(change+"/"+method, func(t *testing.T) {
				f := newProjectMemberAPIFixture(t)
				called := false
				f.server.projectMembers = projectMemberBoundaryRepository{
					ProjectMemberRepository: f.members,
					before: func() {
						called = true
						query := "DELETE FROM project_members WHERE user_id='manager'"
						if change == "disable" {
							query = "UPDATE users SET status='disabled' WHERE id='manager'"
						}
						if _, err := f.db.ExecContext(t.Context(), query); err != nil {
							t.Fatal(err)
						}
					},
				}
				path, body := memberTestPath, ""
				if method == http.MethodPost {
					body = `{"user_id":"target","role":"dns_admin"}`
				} else if method == http.MethodDelete {
					path += "/viewer/roles/read_only"
				}
				response := serveUserRequest(f.server, f.cookies["manager"], method, path, body)
				log := f.lastAudit(t)
				if !called || response.Code != http.StatusForbidden || log.OperatorID != "manager" || log.Status != "failed" {
					t.Fatalf("会话验证后的撤权未拒绝，或丢失已验证身份：%t，%d，%+v", called, response.Code, log)
				}
				var target, viewer int
				if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM project_members WHERE user_id='target'").Scan(&target); err != nil {
					t.Fatal(err)
				}
				if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM project_members WHERE user_id='viewer'").Scan(&viewer); err != nil || target != 0 || viewer != 1 {
					t.Fatalf("撤权后仍修改了成员关系：%d，%d，%v", target, viewer, err)
				}
			})
		}
	}
}

func TestProjectMemberAPICanceledRequestKeepsBoundedFailureAudit(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	audited := false
	f.server.projectMembers = projectMemberBoundaryRepository{
		ProjectMemberRepository: f.members,
		before:                  cancel,
		audit: func(auditContext context.Context) {
			audited = true
			deadline, ok := auditContext.Deadline()
			if ctx.Err() == nil || auditContext.Err() != nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 3*time.Second {
				t.Fatalf("失败审计未脱离取消请求或等待无界：%v，%v，%v", ctx.Err(), auditContext.Err(), deadline)
			}
		},
	}
	request := httptest.NewRequest(http.MethodGet, memberTestPath, nil).WithContext(ctx)
	request.AddCookie(f.cookies["manager"])
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	log := f.lastAudit(t)
	if !audited || response.Code != http.StatusInternalServerError || log.Status != "failed" || log.OperatorID != "manager" {
		t.Fatalf("取消请求导致失败审计丢失：%t，%d，%+v", audited, response.Code, log)
	}
}

func TestProjectMemberAPIFailureAuditStorageUnavailable(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	var output bytes.Buffer
	f.server.logger = &logging.Logger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	previous := f.lastAudit(t)
	if _, err := f.db.ExecContext(t.Context(), `CREATE TRIGGER reject_failed_member_audit BEFORE INSERT ON audit_logs
		WHEN NEW.resource_type='project_member' AND NEW.status='failed'
		BEGIN SELECT RAISE(ABORT, 'sensitive-audit-storage-marker'); END`); err != nil {
		t.Fatal(err)
	}
	response := serveUserRequest(f.server, f.cookies["viewer"], http.MethodGet, memberTestPath, "")
	if response.Code != http.StatusForbidden || f.lastAudit(t).ID != previous.ID {
		t.Fatalf("审计存储失败改变了原拒绝响应或伪造了审计：%d", response.Code)
	}
	if !strings.Contains(output.String(), "项目成员失败审计未保存") || !strings.Contains(output.String(), response.Header().Get(requestIDHeader)) {
		t.Fatalf("审计存储失败没有稳定可关联日志：%s", output.String())
	}
	if strings.Contains(output.String()+response.Body.String(), "sensitive-audit-storage-marker") {
		t.Fatal("审计存储失败泄露底层错误")
	}
}
