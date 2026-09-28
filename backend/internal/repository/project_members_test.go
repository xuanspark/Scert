package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestProjectMembersSQLite(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "members.db")
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	checkProjectMembers(t, db.DB, cfg.Driver)
}

func TestProjectMembersMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	cfg := server.Database(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	checkProjectMembers(t, db.DB, cfg.Driver)
}

func checkProjectMembers(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	ctx := t.Context()
	repo := NewSQLProjectMemberRepository(db, driver)
	projects := NewSQLProjectRepository(db, driver)
	for _, id := range []string{"project-a", "project-b"} {
		if err := projects.CreateProject(ctx, domain.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []struct{ id, role, status string }{
		{"root-test", UserRoleSuperAdmin, UserStatusActive},
		{"manager", UserRoleUser, UserStatusActive},
		{"viewer", UserRoleUser, UserStatusActive},
		{"global-admin", UserRoleAdmin, UserStatusActive},
		{"outsider", UserRoleUser, UserStatusActive},
		{"disabled", UserRoleUser, UserStatusDisabled},
	} {
		_, err := db.ExecContext(ctx, Rebind(driver, `INSERT INTO users (id, username, email, password_hash, role, status, groups_json)
			VALUES (?, ?, ?, 'test-only-no-login', ?, ?, '["same-group"]')`), u.id, u.id, u.id+"@example.invalid", u.role, u.status)
		if err != nil {
			t.Fatal(err)
		}
	}
	grant := func(actor, project, user string, role domain.ProjectRole) {
		t.Helper()
		if err := repo.GrantProjectRole(ctx, actor, project, user, role); err != nil {
			t.Fatal(err)
		}
	}
	check := func(user, project string, permission domain.ProjectPermission, allowed bool) {
		t.Helper()
		err := repo.AuthorizeProject(ctx, user, project, permission)
		if allowed && err != nil {
			t.Fatalf("预期允许 %s/%s/%s：%v", user, project, permission, err)
		}
		if !allowed && !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("预期拒绝 %s/%s/%s：%v", user, project, permission, err)
		}
	}
	t.Run("no_implicit_membership", func(t *testing.T) {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_members").Scan(&count); err != nil || count != 0 {
			t.Fatalf("迁移自动授予了权限：%d，%v", count, err)
		}
		for _, user := range []string{"manager", "global-admin", "outsider", "missing", "disabled"} {
			check(user, "default", domain.PermissionProjectRead, false)
			check(user, "project-a", domain.PermissionProjectRead, false)
		}
		check("root-test", "project-a", domain.PermissionProjectManage, true)
		check("root-test", "missing", domain.PermissionProjectRead, false)
		check("root-test", "project-a", "unknown", false)
	})
	grant("root-test", "project-a", "manager", domain.ProjectRoleAdmin)
	t.Run("project_admin_and_private_key_boundary", func(t *testing.T) {
		check("manager", "project-a", domain.PermissionMembersManage, true)
		check("manager", "project-a", domain.PermissionPrivateKeyRead, false)
		check("manager", "project-b", domain.PermissionProjectRead, false)
		grant("manager", "project-a", "viewer", domain.ProjectRoleReadOnly)
		check("viewer", "project-a", domain.PermissionCertificateRead, true)
		check("viewer", "project-a", domain.PermissionCertificateManage, false)
		check("outsider", "project-a", domain.PermissionCertificateRead, false)
	})
	t.Run("unauthorized_grants_and_invalid_targets", func(t *testing.T) {
		for _, tc := range []struct {
			actor, project, user string
			role                 domain.ProjectRole
		}{
			{"viewer", "project-a", "viewer", domain.ProjectRoleAdmin},
			{"global-admin", "project-a", "viewer", domain.ProjectRoleAdmin},
			{"manager", "project-b", "viewer", domain.ProjectRoleAdmin},
			{"root-test", "missing", "viewer", domain.ProjectRoleAdmin},
			{"root-test", "project-a", "missing", domain.ProjectRoleAdmin},
			{"root-test", "project-a", "disabled", domain.ProjectRoleAdmin},
			{"root-test", "project-a", "viewer", "super_admin"},
		} {
			if err := repo.GrantProjectRole(ctx, tc.actor, tc.project, tc.user, tc.role); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("非法授予未拒绝：%+v，%v", tc, err)
			}
		}
		if err := repo.GrantProjectRole(ctx, "root-test", "project-a", "viewer", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("重复角色未拒绝")
		}
		check("viewer", "project-a", domain.PermissionMembersManage, false)
	})
	t.Run("multiple_roles_and_projects", func(t *testing.T) {
		grant("manager", "project-a", "viewer", domain.ProjectRoleDNSAdmin)
		grant("root-test", "project-b", "viewer", domain.ProjectRoleAuditViewer)
		check("viewer", "project-a", domain.PermissionDNSCredentialUse, true)
		check("viewer", "project-a", domain.PermissionCertificateRead, true)
		check("viewer", "project-a", domain.PermissionAuditRead, false)
		check("viewer", "project-b", domain.PermissionAuditRead, true)
		check("viewer", "project-b", domain.PermissionDNSCredentialUse, false)
	})
	t.Run("revocation_is_immediate_and_scoped", func(t *testing.T) {
		if err := repo.RevokeProjectRole(ctx, "viewer", "project-a", "manager", domain.ProjectRoleAdmin); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("越权撤销未拒绝：%v", err)
		}
		if err := repo.RevokeProjectRole(ctx, "manager", "project-b", "viewer", domain.ProjectRoleAuditViewer); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("跨项目撤销未拒绝：%v", err)
		}
		if err := repo.RevokeProjectRole(ctx, "manager", "project-a", "viewer", domain.ProjectRoleDNSAdmin); err != nil {
			t.Fatal(err)
		}
		check("viewer", "project-a", domain.PermissionDNSCredentialUse, false)
		check("viewer", "project-a", domain.PermissionCertificateRead, true)
		check("viewer", "project-b", domain.PermissionAuditRead, true)
		if err := repo.RevokeProjectRole(ctx, "manager", "project-a", "manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		check("manager", "project-a", domain.PermissionMembersManage, false)
		if err := repo.GrantProjectRole(ctx, "manager", "project-a", "manager", domain.ProjectRoleAdmin); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("撤权后可自行恢复：%v", err)
		}
		grant("root-test", "project-a", "manager", domain.ProjectRoleAdmin)
	})
	t.Run("disabled_and_demoted_accounts", func(t *testing.T) {
		grant("root-test", "project-a", "global-admin", domain.ProjectRoleCertificateAdmin)
		check("global-admin", "project-a", domain.PermissionPrivateKeyRead, true)
		if _, err := db.ExecContext(ctx, "UPDATE users SET role = 'user' WHERE id = 'global-admin'"); err != nil {
			t.Fatal(err)
		}
		check("global-admin", "project-a", domain.PermissionPrivateKeyRead, false)
		check("global-admin", "project-a", domain.PermissionCertificateManage, true)
		if _, err := db.ExecContext(ctx, "UPDATE users SET status = 'disabled' WHERE id = 'manager'"); err != nil {
			t.Fatal(err)
		}
		check("manager", "project-a", domain.PermissionProjectRead, false)
		if err := repo.GrantProjectRole(ctx, "manager", "project-a", "outsider", domain.ProjectRoleReadOnly); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("禁用账户仍可授予：%v", err)
		}
		if err := repo.RevokeProjectRole(ctx, "manager", "project-a", "viewer", domain.ProjectRoleReadOnly); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("禁用账户仍可撤销：%v", err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE users SET status = 'active' WHERE id = 'manager'"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE users SET status = 'disabled' WHERE id = 'root-test'"); err != nil {
			t.Fatal(err)
		}
		check("root-test", "project-a", domain.PermissionMembersManage, false)
		if _, err := db.ExecContext(ctx, "UPDATE users SET status = 'active' WHERE id = 'root-test'"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("parent_references", func(t *testing.T) {
		if err := projects.DeleteProject(ctx, "project-b"); err == nil {
			t.Fatal("删除了仍有成员的项目")
		}
		if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE id = 'viewer'"); err == nil {
			t.Fatal("删除了仍有项目角色的用户")
		}
		for _, query := range []string{
			"INSERT INTO project_members(project_id,user_id,role) VALUES ('missing','viewer','read_only')",
			"INSERT INTO project_members(project_id,user_id,role) VALUES ('project-a','missing','read_only')",
			"UPDATE project_members SET user_id='missing' WHERE user_id='viewer'",
			"UPDATE project_members SET project_id='missing' WHERE user_id='viewer'",
		} {
			if _, err := db.ExecContext(ctx, query); err == nil {
				t.Fatalf("无效引用未拒绝：%s", query)
			}
		}
		if err := repo.RevokeProjectRole(ctx, "root-test", "project-b", "viewer", domain.ProjectRoleAuditViewer); err != nil {
			t.Fatal(err)
		}
		if err := projects.DeleteProject(ctx, "project-b"); err != nil {
			t.Fatal(err)
		}
		check("viewer", "project-b", domain.PermissionAuditRead, false)
	})
	t.Run("canceled_context", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := repo.AuthorizeProject(canceled, "root-test", "project-a", domain.PermissionProjectRead); err == nil {
			t.Fatal("取消请求意外授权")
		}
		if err := repo.GrantProjectRole(canceled, "root-test", "project-a", "outsider", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("取消请求意外写入")
		}
	})
}

func TestProjectMembersNilDatabase(t *testing.T) {
	for _, repo := range []*SQLProjectMemberRepository{nil, NewSQLProjectMemberRepository(nil)} {
		if err := repo.AuthorizeProject(t.Context(), "user", "project", domain.PermissionProjectRead); err == nil {
			t.Fatal("空连接未拒绝")
		}
		if err := repo.GrantProjectRole(t.Context(), "actor", "project", "user", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("空连接未拒绝")
		}
		if err := repo.RevokeProjectRole(t.Context(), "actor", "project", "user", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("空连接未拒绝")
		}
	}
}
