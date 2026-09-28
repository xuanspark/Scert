package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// 复用真实资源与六类项目成员夹具；由 SQLite 和隔离 MySQL 入口分别调用。
func checkProjectBindings(t *testing.T, db, admin *sql.DB, repo *SQLProjectMemberRepository, driver string) {
	t.Helper()
	unchanged := func(t *testing.T) {
		t.Helper()
		items, err := NewSQLServerRepository(db, driver).ListServersByDomainID(t.Context(), "domain-a")
		if err != nil || len(items) != 1 || items[0].ID != "server-a" {
			t.Fatalf("原绑定未保留：%+v，%v", items, err)
		}
	}
	t.Run("bindings_role_matrix_and_audit", func(t *testing.T) {
		for _, actor := range []string{"root", "manager", "deploy", "cert", "dns", "audit", "reader", "outsider", "missing"} {
			items, err := repo.ReplaceProjectDomainServers(t.Context(), actor, "domain-a", []string{"server-a", "server-a"})
			allowed := actor == "root" || actor == "manager" || actor == "deploy"
			if !allowed {
				if !errors.Is(err, ErrProjectAccessDenied) {
					t.Fatalf("未授权角色仍可写绑定：%s，%v", actor, err)
				}
				continue
			}
			if err != nil || len(items) != 1 || items[0].ID != "server-a" {
				t.Fatalf("授权绑定失败或未去重：%s，%+v，%v", actor, items, err)
			}
			var count int
			if err := db.QueryRowContext(t.Context(), Rebind(driver, `SELECT COUNT(*) FROM audit_logs
				WHERE action='domain.servers.replace' AND status='success' AND project_id='project-a'
				AND resource_id='domain-a' AND operator_id=?`), actor).Scan(&count); err != nil || count != 1 {
				t.Fatalf("成功审计范围或操作者错误：%s，%d，%v", actor, count, err)
			}
		}
		unchanged(t)
	})
	t.Run("bindings_cross_scope_and_unknown_preserve_existing", func(t *testing.T) {
		for _, actor := range []string{"manager", "root"} {
			for _, id := range []string{"server-b", "missing", "server-a' OR 1=1 --"} {
				if _, err := repo.ReplaceProjectDomainServers(t.Context(), actor, "domain-a", []string{"server-a", id}); !errors.Is(err, ErrProjectAccessDenied) {
					t.Fatalf("越权、未知或注入资源未拒绝：%s，%s，%v", actor, id, err)
				}
				unchanged(t)
			}
		}
		for _, id := range []string{"domain-b", "missing"} {
			if _, err := repo.ReplaceProjectDomainServers(t.Context(), "manager", id, []string{}); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("未知或越权域名允许清空：%s，%v", id, err)
			}
		}
	})
	t.Run("bindings_explicit_empty_and_invalid_input", func(t *testing.T) {
		items, err := repo.ReplaceProjectDomainServers(t.Context(), "manager", "domain-a", []string{})
		if err != nil || items == nil || len(items) != 0 {
			t.Fatalf("明确清空未返回空列表：%+v，%v", items, err)
		}
		loaded, err := NewSQLServerRepository(db, driver).ListServersByDomainID(t.Context(), "domain-a")
		if err != nil || len(loaded) != 0 {
			t.Fatalf("清空未持久化：%+v，%v", loaded, err)
		}
		if _, err := repo.ReplaceProjectDomainServers(t.Context(), "manager", "domain-a", []string{"server-a"}); err != nil {
			t.Fatal(err)
		}
		for _, ids := range [][]string{nil, {""}, {" server-a"}, make([]string, MaxProjectBindingServers+1)} {
			if _, err := repo.ReplaceProjectDomainServers(t.Context(), "manager", "domain-a", ids); !errors.Is(err, ErrProjectBindingInput) {
				t.Fatalf("非法列表未拒绝：%v", err)
			}
			unchanged(t)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := repo.ReplaceProjectDomainServers(ctx, "root", "domain-a", []string{}); err == nil {
			t.Fatal("取消后仍修改了绑定")
		}
		unchanged(t)
	})
	t.Run("bindings_audit_failure_rolls_back", func(t *testing.T) {
		executor := db
		triggerName := "reject_binding_audit"
		trigger := `CREATE TRIGGER reject_binding_audit BEFORE INSERT ON audit_logs
			BEGIN SELECT RAISE(ABORT, 'audit-failure'); END`
		if driver == "mysql" {
			var schema string
			if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			// 仅隔离测试管理员建触发器，不提升应用账户权限。
			triggerName = "`" + strings.ReplaceAll(schema, "`", "``") + "`.reject_binding_audit"
			trigger = "CREATE TRIGGER " + triggerName + " BEFORE INSERT ON `" + strings.ReplaceAll(schema, "`", "``") +
				"`.audit_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='audit-failure'"
			executor = admin
		}
		if _, err := executor.ExecContext(t.Context(), trigger); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := executor.ExecContext(context.Background(), "DROP TRIGGER "+triggerName); err != nil {
				t.Error(err)
			}
		})
		if _, err := repo.ReplaceProjectDomainServers(t.Context(), "manager", "domain-a", []string{}); err == nil {
			t.Fatal("审计失败时仍提交了清空操作")
		}
		unchanged(t)
	})
	if driver == "sqlite" {
		t.Run("bindings_ignored_audit_rolls_back", func(t *testing.T) {
			if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER ignore_binding_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(IGNORE); END`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TRIGGER ignore_binding_audit") })
			if _, err := repo.ReplaceProjectDomainServers(t.Context(), "manager", "domain-a", []string{}); err == nil {
				t.Fatal("审计未落库仍返回成功")
			}
			unchanged(t)
		})
	}
}
