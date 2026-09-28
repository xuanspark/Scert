package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

type memberChangeFixture struct {
	db     *sql.DB
	repo   *SQLProjectMemberRepository
	driver string
	admin  *sql.DB
}

func TestProjectMemberChangesSQLite(t *testing.T) {
	checkProjectMemberChanges(t, func(t *testing.T) config.DatabaseConfig {
		cfg := config.Default(t.TempDir()).Database
		cfg.DSN = filepath.Join(t.TempDir(), "member-audit.db")
		return cfg
	}, nil)
}

func TestProjectMemberChangesMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkProjectMemberChanges(t, server.Database, server.Admin())
}

func checkProjectMemberChanges(t *testing.T, makeConfig func(*testing.T) config.DatabaseConfig, admin *sql.DB) {
	t.Helper()
	run := func(name string, test func(*testing.T, memberChangeFixture)) {
		t.Run(name, func(t *testing.T) {
			cfg := makeConfig(t)
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
				t.Fatal(err)
			}
			projects := NewSQLProjectRepository(db.DB, cfg.Driver)
			if err := projects.CreateProject(t.Context(), domain.Project{ID: "audit-project", Name: "审计测试项目"}); err != nil {
				t.Fatal(err)
			}
			for _, u := range []struct{ id, role, status string }{
				{"audit-root", UserRoleSuperAdmin, UserStatusActive},
				{"audit-manager", UserRoleUser, UserStatusActive},
				{"audit-target", UserRoleUser, UserStatusActive},
				{"audit-disabled", UserRoleUser, UserStatusDisabled},
			} {
				_, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users
					(id, username, email, password_hash, role, status) VALUES (?, ?, ?, 'test-only-no-login', ?, ?)`),
					u.id, u.id, u.id+"@example.invalid", u.role, u.status)
				if err != nil {
					t.Fatal(err)
				}
			}
			// 夹具直接准备初始管理员，待验证的业务变更一律走正式仓储方法。
			if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO project_members (project_id, user_id, role)
				VALUES ('audit-project', 'audit-manager', 'project_admin')`); err != nil {
				t.Fatal(err)
			}
			test(t, memberChangeFixture{db: db.DB, repo: NewSQLProjectMemberRepository(db.DB, cfg.Driver), driver: cfg.Driver, admin: admin})
		})
	}
	memberChangeSuccessCases(t, run)
	memberChangeFailureCases(t, run)
	memberChangeHistoryCases(t, run)
}

func memberChangeSuccessCases(t *testing.T, run func(string, func(*testing.T, memberChangeFixture))) {
	t.Helper()
	run("grant_and_revoke", func(t *testing.T, f memberChangeFixture) {
		before := time.Now().UTC().Add(-time.Second)
		if err := f.repo.GrantProjectRole(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleDNSAdmin); err != nil {
			t.Fatal(err)
		}
		if err := f.repo.RevokeProjectRole(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleDNSAdmin); err != nil {
			t.Fatal(err)
		}
		assertMemberAudit(t, f, 2, 0)
		rows, err := f.db.QueryContext(t.Context(), `SELECT action, resource_type, resource_id, status, message,
			created_at, scope_type, project_id, task_id, operator_id FROM audit_logs ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for _, action := range []string{"project_member.grant", "project_member.revoke"} {
			var item AuditLog
			if !rows.Next() {
				t.Fatalf("缺少成员审计：%s", action)
			}
			if err := rows.Scan(&item.Action, &item.ResourceType, &item.ResourceID, &item.Status, &item.Message,
				&item.CreatedAt, &item.ScopeType, &item.ProjectID, &item.TaskID, &item.OperatorID); err != nil {
				t.Fatal(err)
			}
			if item.Action != action || item.ResourceType != "project_member" || item.ResourceID != "audit-target" ||
				item.Status != "success" || item.ScopeType != ScopeProject || item.ProjectID != "audit-project" ||
				item.OperatorID != "audit-manager" || item.TaskID != "" || !strings.Contains(item.Message, "dns_admin") ||
				item.CreatedAt.Before(before) || item.CreatedAt.After(time.Now().UTC().Add(time.Second)) {
				t.Fatalf("成员审计范围、角色或身份错误：%+v", item)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
	run("no_success_on_rejection", func(t *testing.T, f memberChangeFixture) {
		for _, actor := range []string{"audit-target", "audit-disabled", "missing"} {
			if err := f.repo.GrantProjectRole(t.Context(), actor, "audit-project", "audit-target", domain.ProjectRoleAdmin); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("无权限的授予未拒绝：%v", err)
			}
		}
		if err := f.repo.GrantProjectRole(t.Context(), "audit-root", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err != nil {
			t.Fatal(err)
		}
		if err := f.repo.GrantProjectRole(t.Context(), "audit-root", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("重复授予意外成功")
		}
		if err := f.repo.RevokeProjectRole(t.Context(), "audit-root", "audit-project", "audit-target", domain.ProjectRoleDNSAdmin); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("不存在的角色撤销未拒绝：%v", err)
		}
		assertMemberAudit(t, f, 1, 1)
	})
}

func memberChangeFailureCases(t *testing.T, run func(string, func(*testing.T, memberChangeFixture))) {
	t.Helper()
	run("grant_audit_failure", func(t *testing.T, f memberChangeFixture) {
		failMemberAudit(t, f)
		if err := f.repo.GrantProjectRole(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("审计失败仍返回授予成功")
		}
		assertMemberAudit(t, f, 0, 0)
	})
	run("revoke_audit_failure", func(t *testing.T, f memberChangeFixture) {
		if err := f.repo.GrantProjectRole(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err != nil {
			t.Fatal(err)
		}
		failMemberAudit(t, f)
		if err := f.repo.RevokeProjectRole(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("审计失败仍返回撤销成功")
		}
		assertMemberAudit(t, f, 1, 1)
	})
	run("self_revoke_and_recovery", func(t *testing.T, f memberChangeFixture) {
		restore := failMemberAudit(t, f)
		if err := f.repo.RevokeProjectRole(t.Context(), "audit-manager", "audit-project", "audit-manager", domain.ProjectRoleAdmin); err == nil {
			t.Fatal("审计失败仍撤销了自己的角色")
		}
		if err := f.repo.AuthorizeProject(t.Context(), "audit-manager", "audit-project", domain.PermissionMembersManage); err != nil {
			t.Fatalf("审计失败没有恢复原权限：%v", err)
		}
		restore()
		if err := f.repo.RevokeProjectRole(t.Context(), "audit-manager", "audit-project", "audit-manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := f.repo.GrantProjectRole(t.Context(), "audit-manager", "audit-project", "audit-manager", domain.ProjectRoleAdmin); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("自我撤权后仍可恢复：%v", err)
		}
		if err := f.repo.GrantProjectRole(t.Context(), "audit-root", "audit-project", "audit-manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		assertMemberAudit(t, f, 2, 0)
	})
	run("audit_storage_fail_closed", func(t *testing.T, f memberChangeFixture) {
		query := `CREATE TRIGGER ignore_member_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(IGNORE); END`
		if f.driver == "mysql" {
			query = "ALTER TABLE audit_logs ENGINE=MyISAM"
		}
		if _, err := f.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
		if err := f.repo.GrantProjectRole(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("审计被忽略或不可回滚时仍接受了成员变更")
		}
		assertMemberAudit(t, f, 0, 0)
	})
}

func memberChangeHistoryCases(t *testing.T, run func(string, func(*testing.T, memberChangeFixture))) {
	t.Helper()
	run("concurrent_duplicate_changes", func(t *testing.T, f memberChangeFixture) {
		for _, change := range []func(context.Context, string, string, string, domain.ProjectRole) error{
			f.repo.GrantProjectRole, f.repo.RevokeProjectRole,
		} {
			var workers sync.WaitGroup
			results := make(chan error, 6)
			for range 6 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					results <- change(t.Context(), "audit-manager", "audit-project", "audit-target", domain.ProjectRoleReadOnly)
				}()
			}
			workers.Wait()
			close(results)
			succeeded := 0
			for err := range results {
				if err == nil {
					succeeded++
				}
			}
			if succeeded != 1 {
				t.Fatalf("并发重复变更成功次数错误：%d", succeeded)
			}
		}
		assertMemberAudit(t, f, 2, 0)
	})
	run("history_survives_deletion", func(t *testing.T, f memberChangeFixture) {
		for _, change := range []func(context.Context, string, string, string, domain.ProjectRole) error{
			f.repo.GrantProjectRole, f.repo.RevokeProjectRole,
		} {
			if err := change(t.Context(), "audit-root", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.repo.RevokeProjectRole(t.Context(), "audit-root", "audit-project", "audit-manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := NewSQLProjectRepository(f.db, f.driver).DeleteProject(t.Context(), "audit-project"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), "DELETE FROM users WHERE id IN ('audit-root', 'audit-target')"); err != nil {
			t.Fatal(err)
		}
		assertMemberAudit(t, f, 3, 0)
		var count int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_logs WHERE scope_type='project'
			AND project_id='audit-project' AND operator_id='audit-root'`).Scan(&count); err != nil || count != 3 {
			t.Fatalf("历史项目或操作者快照丢失：%d，%v", count, err)
		}
	})
	run("canceled_context", func(t *testing.T, f memberChangeFixture) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := f.repo.GrantProjectRole(ctx, "audit-manager", "audit-project", "audit-target", domain.ProjectRoleReadOnly); err == nil {
			t.Fatal("已取消的授予仍然成功")
		}
		if err := f.repo.RevokeProjectRole(ctx, "audit-root", "audit-project", "audit-manager", domain.ProjectRoleAdmin); err == nil {
			t.Fatal("已取消的撤销仍然成功")
		}
		assertMemberAudit(t, f, 0, 0)
	})
}

func failMemberAudit(t *testing.T, f memberChangeFixture) func() {
	t.Helper()
	executor := f.db
	drop := "DROP TRIGGER fail_project_member_audit"
	query := `CREATE TRIGGER fail_project_member_audit BEFORE INSERT ON audit_logs
		WHEN NEW.resource_type='project_member' BEGIN SELECT RAISE(ABORT, 'test-only-audit-failure'); END`
	if f.driver == "mysql" {
		// 故障触发器由隔离实例管理员创建，不提升业务账户权限或放宽二进制日志策略。
		var schema string
		if err := f.db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
		executor = f.admin
		query = `CREATE TRIGGER ` + schema + `.fail_project_member_audit BEFORE INSERT ON ` + schema + `.audit_logs FOR EACH ROW
			SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test-only-audit-failure'`
		drop = "DROP TRIGGER " + schema + ".fail_project_member_audit"
	}
	if _, err := executor.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if _, err := executor.ExecContext(t.Context(), drop); err != nil {
			t.Fatal(err)
		}
	}
}

func assertMemberAudit(t *testing.T, f memberChangeFixture, auditCount, memberCount int) {
	t.Helper()
	var actual int
	if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs").Scan(&actual); err != nil || actual != auditCount {
		t.Fatalf("审计数量错误，期望 %d，实际 %d：%v", auditCount, actual, err)
	}
	if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM project_members WHERE user_id='audit-target'").Scan(&actual); err != nil || actual != memberCount {
		t.Fatalf("成员数量错误，期望 %d，实际 %d：%v", memberCount, actual, err)
	}
}
