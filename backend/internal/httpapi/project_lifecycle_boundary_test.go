package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type projectLifecycleBoundaryRepository struct {
	repository.ProjectLifecycleRepository
	before func()
	audit  func(context.Context)
}

func (r projectLifecycleBoundaryRepository) CreateManagedProject(ctx context.Context, actor, name, description string) (domain.Project, error) {
	r.before()
	return r.ProjectLifecycleRepository.CreateManagedProject(ctx, actor, name, description)
}

func (r projectLifecycleBoundaryRepository) UpdateManagedProject(ctx context.Context, actor, id, name, description string) error {
	r.before()
	return r.ProjectLifecycleRepository.UpdateManagedProject(ctx, actor, id, name, description)
}

func (r projectLifecycleBoundaryRepository) DeleteManagedProject(ctx context.Context, actor, id string) error {
	r.before()
	return r.ProjectLifecycleRepository.DeleteManagedProject(ctx, actor, id)
}

func (r projectLifecycleBoundaryRepository) RecordProjectFailure(ctx context.Context, failure repository.ProjectFailure) error {
	if r.audit != nil {
		r.audit(ctx)
	}
	return r.ProjectLifecycleRepository.RecordProjectFailure(ctx, failure)
}

func TestProjectLifecycleAPIRechecksAfterAuthentication(t *testing.T) {
	for _, change := range []string{"disable", "demote"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			t.Run(change+method, func(t *testing.T) {
				f := newProjectMemberAPIFixture(t)
				called := false
				f.server.projectLifecycle = projectLifecycleBoundaryRepository{ProjectLifecycleRepository: f.members, before: func() {
					called = true
					query := "UPDATE users SET status='disabled' WHERE id='primary-super-admin'"
					if change == "demote" {
						query = "UPDATE users SET role='admin' WHERE id='primary-super-admin'"
					}
					if _, err := f.db.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}}
				path, body := "/api/v1/projects", `{"name":"不能保存"}`
				if method != http.MethodPost {
					path += "/project-b"
				}
				if method == http.MethodDelete {
					body = `{"confirmation":"project-b"}`
				}
				response := serveUserRequest(f.server, f.cookies["primary-super-admin"], method, path, body)
				log := f.lastAudit(t)
				if !called || response.Code != http.StatusForbidden || log.OperatorID != "primary-super-admin" || log.Status != "failed" {
					t.Fatalf("会话后的权限变化未生效：%d，%+v", response.Code, log)
				}
				var count int
				if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 3 {
					t.Fatalf("撤权后项目发生变化：%d，%v", count, err)
				}
			})
		}
	}
	t.Run("project_role_revoked", func(t *testing.T) {
		f := newProjectMemberAPIFixture(t)
		f.server.projectLifecycle = projectLifecycleBoundaryRepository{ProjectLifecycleRepository: f.members, before: func() {
			if _, err := f.db.ExecContext(t.Context(), "DELETE FROM project_members WHERE user_id='manager'"); err != nil {
				t.Fatal(err)
			}
		}}
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPut, "/api/v1/projects/project-a", `{"name":"不能保存"}`)
		if response.Code != 403 {
			t.Fatalf("项目角色撤销后仍允许修改：%d", response.Code)
		}
	})
}

func TestProjectLifecycleAPICanceledRequestKeepsBoundedAudit(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	audited := false
	f.server.projectLifecycle = projectLifecycleBoundaryRepository{
		ProjectLifecycleRepository: f.members, before: cancel,
		audit: func(ctx context.Context) {
			audited = true
			deadline, ok := ctx.Deadline()
			if ctx.Err() != nil || !ok || time.Until(deadline) > 3*time.Second {
				t.Fatal("失败审计没有独立有界上下文")
			}
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"取消项目"}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(f.cookies["primary-super-admin"])
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	if !audited || response.Code != 500 || f.lastAudit(t).Status != "failed" {
		t.Fatalf("取消请求后审计丢失：%d", response.Code)
	}
}
