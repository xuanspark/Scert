package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestProjectMemberListingSQLite(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "project-members.db")
	checkProjectMemberListing(t, cfg)
}

func TestProjectMemberListingMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkProjectMemberListing(t, server.Database(t))
}

func checkProjectMemberListing(t *testing.T, cfg config.DatabaseConfig) {
	t.Helper()
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	for _, id := range []string{"page-project", "empty-project"} {
		if err := projects.CreateProject(t.Context(), domain.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []struct{ id, role string }{
		{"root", UserRoleSuperAdmin}, {"manager", UserRoleUser}, {"global-admin", UserRoleAdmin},
	} {
		_, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users (id, username, password_hash, role, status)
			VALUES (?, ?, 'test-only-no-login', ?, 'active')`), user.id, user.id, user.role)
		if err != nil {
			t.Fatal(err)
		}
	}
	repo := NewSQLProjectMemberRepository(db.DB, cfg.Driver)
	if err := repo.GrantProjectRole(t.Context(), "root", "page-project", "manager", domain.ProjectRoleAdmin); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("user-%02d", i)
		if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users (id, username, password_hash, role, status)
			VALUES (?, ?, 'test-only-no-login', 'user', 'active')`), id, id); err != nil {
			t.Fatal(err)
		}
		for _, role := range []domain.ProjectRole{domain.ProjectRoleReadOnly, domain.ProjectRoleDNSAdmin} {
			if err := repo.GrantProjectRole(t.Context(), "root", "page-project", id, role); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Run("pagination_keeps_roles_together", func(t *testing.T) {
		for _, size := range []int{1, 5, 10, 15, 30} {
			after := ""
			seen := make(map[string]bool)
			for pages := 0; ; pages++ {
				if pages > 13 {
					t.Fatal("分页游标未前进")
				}
				page, err := repo.ListProjectMembers(t.Context(), "manager", "page-project", after, size)
				if err != nil || page.Items == nil || len(page.Items) > size || page.PageSize != size {
					t.Fatalf("成员分页错误：%+v，%v", page, err)
				}
				for _, member := range page.Items {
					if seen[member.UserID] || member.Username != member.UserID || member.Status != UserStatusActive {
						t.Fatalf("成员重复或身份字段错误：%+v", member)
					}
					seen[member.UserID] = true
					if member.UserID != "manager" && (len(member.Roles) != 2 || member.Roles[0] != domain.ProjectRoleDNSAdmin || member.Roles[1] != domain.ProjectRoleReadOnly) {
						t.Fatalf("多角色被拆分或顺序不稳定：%+v", member)
					}
				}
				if !page.HasMore {
					if page.NextCursor != "" || len(seen) != 13 {
						t.Fatalf("末页游标或累计人数错误：%+v，%d", page, len(seen))
					}
					break
				}
				if len(page.Items) != size || page.NextCursor != page.Items[size-1].UserID {
					t.Fatalf("下一页游标错误：%+v", page)
				}
				after = page.NextCursor
			}
		}
	})
	t.Run("empty_and_forbidden", func(t *testing.T) {
		for _, tc := range []struct{ actor, project string }{
			{"global-admin", "page-project"}, {"user-00", "page-project"}, {"manager", "empty-project"}, {"root", "missing-project"}, {"missing-actor", "page-project"},
		} {
			page, err := repo.ListProjectMembers(t.Context(), tc.actor, tc.project, "", 5)
			if !errors.Is(err, ErrProjectAccessDenied) || len(page.Items) != 0 {
				t.Fatalf("未授权列表泄露：%+v，%+v，%v", tc, page, err)
			}
		}
		for _, tc := range []struct{ project, after string }{{"empty-project", ""}, {"page-project", "zzzz"}} {
			page, err := repo.ListProjectMembers(t.Context(), "root", tc.project, tc.after, 5)
			if err != nil || page.Items == nil || len(page.Items) != 0 || page.HasMore || page.NextCursor != "" {
				t.Fatalf("有效空页被误拒绝：%+v，%v", page, err)
			}
		}
	})
	t.Run("disabled_and_revoked", func(t *testing.T) {
		if _, err := db.DB.ExecContext(t.Context(), "UPDATE users SET status='disabled' WHERE id='manager'"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ListProjectMembers(t.Context(), "manager", "page-project", "", 5); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("停用账户仍能读取成员：%v", err)
		}
		if _, err := db.DB.ExecContext(t.Context(), "UPDATE users SET status='active' WHERE id='manager'"); err != nil {
			t.Fatal(err)
		}
		if err := repo.RevokeProjectRole(t.Context(), "manager", "page-project", "manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ListProjectMembers(t.Context(), "manager", "page-project", "", 5); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("自我撤权后仍能读取成员：%v", err)
		}
	})
	t.Run("failure_scope_and_identity", func(t *testing.T) {
		checkProjectMemberFailureRecords(t, db.DB, repo, cfg.Driver)
	})
}

func checkProjectMemberFailureRecords(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
	t.Helper()
	for _, project := range []string{"page-project", "missing-project"} {
		for _, actor := range []string{"manager", ""} {
			failure := ProjectMemberFailure{ActorID: actor, ProjectID: project, UserID: "user-00", Action: "project_member.grant", StatusCode: 403, RequestID: "request-test"}
			if err := repo.RecordProjectMemberFailure(t.Context(), failure); err != nil {
				t.Fatal(err)
			}
			var gotActor, scope, gotProject, message, status string
			if err := db.QueryRowContext(t.Context(), "SELECT operator_id, scope_type, project_id, message, status FROM audit_logs ORDER BY id DESC LIMIT 1").Scan(&gotActor, &scope, &gotProject, &message, &status); err != nil {
				t.Fatal(err)
			}
			wantScope, wantProject := ScopeProject, project
			if project == "missing-project" {
				wantScope, wantProject = ScopeUnknown, ""
			}
			if gotActor != actor || scope != wantScope || gotProject != wantProject || status != "failed" || message != "请求被拒绝或执行失败：HTTP 403；请求编号：request-test" {
				t.Fatalf("失败审计范围或身份错误：%s %s %s %s %s", gotActor, scope, gotProject, status, message)
			}
		}
	}
	if err := repo.RecordProjectMemberFailure(t.Context(), ProjectMemberFailure{Action: "project_member.grant", StatusCode: 200}); err == nil {
		t.Fatal("失败审计不应接受成功状态")
	}
}
