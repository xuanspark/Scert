package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestProjectLifecycleSQLite(t *testing.T) {
	checkProjectLifecycle(t, func(t *testing.T) config.DatabaseConfig {
		cfg := config.Default(t.TempDir()).Database
		cfg.DSN = filepath.Join(t.TempDir(), "project-lifecycle.db")
		return cfg
	}, nil)
}

func TestProjectLifecycleMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkProjectLifecycle(t, server.Database, server.Admin())
}

func checkProjectLifecycle(t *testing.T, makeConfig func(*testing.T) config.DatabaseConfig, admin *sql.DB) {
	t.Helper()
	run := func(name string, check func(*testing.T, *sql.DB, *SQLProjectMemberRepository, string)) {
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
			for _, user := range []struct{ id, role, status string }{
				{"root", "super_admin", "active"}, {"manager", "user", "active"},
				{"global-admin", "admin", "active"}, {"viewer", "user", "active"}, {"disabled", "super_admin", "disabled"},
			} {
				_, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users
					(id, username, email, password_hash, role, status) VALUES (?, ?, ?, 'test-only-no-login', ?, ?)`),
					user.id, user.id, user.id+"@example.invalid", user.role, user.status)
				if err != nil {
					t.Fatal(err)
				}
			}
			check(t, db.DB, NewSQLProjectMemberRepository(db.DB, cfg.Driver), cfg.Driver)
		})
	}
	run("create_update_delete_and_history", func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
		item, err := repo.CreateManagedProject(t.Context(), "root", "  项目一  ", "说明")
		if err != nil || item.ID == "" || item.ID == domain.DefaultProjectID || item.Name != "项目一" {
			t.Fatalf("创建失败：%+v，%v", item, err)
		}
		projects := NewSQLProjectRepository(db, driver)
		before, err := projects.GetProject(t.Context(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := repo.UpdateManagedProject(t.Context(), "root", item.ID, "项目二", "新说明"); err != nil {
				t.Fatal(err)
			}
		}
		after, err := projects.GetProject(t.Context(), item.ID)
		if err != nil || !after.CreatedAt.Equal(before.CreatedAt) || after.ID != before.ID || after.Name != "项目二" {
			t.Fatalf("更新破坏身份：%+v，%v", after, err)
		}
		if err := repo.DeleteManagedProject(t.Context(), "root", item.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := projects.GetProject(t.Context(), item.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("项目未删除：%v", err)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), Rebind(driver, `SELECT COUNT(*) FROM audit_logs WHERE resource_type='project'
			AND resource_id=? AND project_id=? AND scope_type='project' AND operator_id='root' AND status='success'`), item.ID, item.ID).Scan(&count); err != nil || count != 4 {
			t.Fatalf("删除后丢失真实审计：%d，%v", count, err)
		}
	})
	for _, actor := range []string{"", "missing", "manager", "global-admin", "viewer", "disabled"} {
		run("create_denied_"+actor, func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
			if _, err := repo.CreateManagedProject(t.Context(), actor, "无权创建", "不应保存"); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("错误授权：%v", err)
			}
			var count int
			if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM projects`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("拒绝后产生项目：%d，%v", count, err)
			}
		})
	}
	run("project_manager_and_revocation", func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
		item, err := repo.CreateManagedProject(t.Context(), "root", "权限测试", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.GrantProjectRole(t.Context(), "root", item.ID, "manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := repo.UpdateManagedProject(t.Context(), "manager", item.ID, "合法修改", ""); err != nil {
			t.Fatal(err)
		}
		for _, actor := range []string{"viewer", "global-admin", "disabled"} {
			if err := repo.UpdateManagedProject(t.Context(), actor, item.ID, "越权修改", ""); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("错误更新授权：%s，%v", actor, err)
			}
			if err := repo.DeleteManagedProject(t.Context(), actor, item.ID); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("错误删除授权：%s，%v", actor, err)
			}
		}
		if err := repo.DeleteManagedProject(t.Context(), "manager", item.ID); !errors.Is(err, ErrProjectReferenced) {
			t.Fatalf("不能自动清理成员：%v", err)
		}
		if err := repo.RevokeProjectRole(t.Context(), "root", item.ID, "manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := repo.UpdateManagedProject(t.Context(), "manager", item.ID, "撤权后修改", ""); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("撤权未生效：%v", err)
		}
		if err := repo.DeleteManagedProject(t.Context(), "manager", item.ID); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("撤权后仍可删除：%v", err)
		}
		if err := repo.DeleteManagedProject(t.Context(), "root", item.ID); err != nil {
			t.Fatal(err)
		}
	})
	for _, kind := range []string{"domain", "server", "member"} {
		run("reference_"+kind, func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
			item, err := repo.CreateManagedProject(t.Context(), "root", "引用保护", "")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "domain":
				resource, err := domain.NewDomain("lifecycle.example.test", domain.CertificateSingle, nil)
				if err != nil {
					t.Fatal(err)
				}
				resource.ProjectID = item.ID
				if err := NewSQLDomainRepository(db, driver).Create(t.Context(), resource); err != nil {
					t.Fatal(err)
				}
			case "server":
				resource := deployment.Server{ID: "server-a", Name: "项目服务器", Address: "192.0.2.10", Port: 22, Username: "deploy",
					AuthMethod: deployment.SSHAuthPassword, PasswordRef: "secret://ssh/password/test", HostKeyFingerprint: "SHA256:test", CertificatePath: "/test/cert.pem",
					PrivateKeyPath: "/test/key.pem", CertificateChainPath: "/test/chain.pem", WebServer: deployment.WebServerNginx}
				resource.ProjectID = item.ID
				if err := NewSQLServerRepository(db, driver).CreateServer(t.Context(), resource); err != nil {
					t.Fatal(err)
				}
			case "member":
				if err := repo.GrantProjectRole(t.Context(), "root", item.ID, "viewer", domain.ProjectRoleReadOnly); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.DeleteManagedProject(t.Context(), "root", item.ID); !errors.Is(err, ErrProjectReferenced) {
				t.Fatalf("引用保护失败：%v", err)
			}
			if _, err := NewSQLProjectRepository(db, driver).GetProject(t.Context(), item.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	run("default_missing_and_cancelled", func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
		if err := repo.DeleteManagedProject(t.Context(), "root", domain.DefaultProjectID); !errors.Is(err, ErrDefaultProject) {
			t.Fatalf("默认项目删除：%v", err)
		}
		if err := repo.DeleteManagedProject(t.Context(), "viewer", domain.DefaultProjectID); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("默认项目泄露权限：%v", err)
		}
		if err := repo.DeleteManagedProject(t.Context(), "root", "missing"); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("不存在项目：%v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := repo.CreateManagedProject(ctx, "root", "取消", ""); err == nil {
			t.Fatal("取消后创建成功")
		}
	})
	for _, action := range []string{"create", "update", "delete"} {
		run("audit_rollback_"+action, func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
			item, err := repo.CreateManagedProject(t.Context(), "root", "必须保留", "")
			if err != nil {
				t.Fatal(err)
			}
			executor := db
			trigger := `CREATE TRIGGER fail_project_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit-failure'); END`
			if driver == "mysql" {
				// 故障注入由隔离实例管理员执行，不提高业务权限或放宽二进制日志策略。
				var schema string
				if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil {
					t.Fatal(err)
				}
				schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
				executor = admin
				trigger = `CREATE TRIGGER ` + schema + `.fail_project_audit BEFORE INSERT ON ` + schema + `.audit_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='audit-failure'`
			}
			if _, err := executor.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "create":
				_, err = repo.CreateManagedProject(t.Context(), "root", "不能保存", "")
			case "update":
				err = repo.UpdateManagedProject(t.Context(), "root", item.ID, "不能保存", "")
			case "delete":
				err = repo.DeleteManagedProject(t.Context(), "root", item.ID)
			}
			if err == nil {
				t.Fatal("审计异常仍返回成功")
			}
			loaded, err := NewSQLProjectRepository(db, driver).GetProject(t.Context(), item.ID)
			if err != nil || loaded.Name != "必须保留" {
				t.Fatalf("项目未回滚：%+v，%v", loaded, err)
			}
			var count int
			if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 2 {
				t.Fatalf("产生部分写入：%d，%v", count, err)
			}
		})
	}
	run("nontransactional_audit_is_rejected", func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
		if driver != "mysql" {
			t.Skip("非事务引擎边界仅适用于 MySQL")
		}
		if _, err := db.ExecContext(t.Context(), "ALTER TABLE audit_logs ENGINE=MyISAM"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateManagedProject(t.Context(), "root", "不能保存", ""); err == nil {
			t.Fatal("非事务审计表仍被允许")
		}
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 1 {
			t.Fatalf("非事务场景产生变更：%d，%v", count, err)
		}
	})
	run("duplicate_names_and_validation", func(t *testing.T, db *sql.DB, repo *SQLProjectMemberRepository, driver string) {
		a, err := repo.CreateManagedProject(t.Context(), "root", "同名", "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := repo.CreateManagedProject(t.Context(), "root", "同名", "")
		if err != nil || a.ID == b.ID {
			t.Fatalf("重名项目没有独立标识：%v", err)
		}
		for _, name := range []string{"", strings.Repeat("中", 129), "控制\x00字符"} {
			if _, err := repo.CreateManagedProject(t.Context(), "root", name, ""); err == nil {
				t.Fatal("无效名称创建成功")
			}
		}
	})
}

func TestProjectLifecycleSQLiteAuditRollback(t *testing.T) {
	for _, action := range []string{"create", "update", "delete"} {
		for _, mode := range []string{"ABORT, 'audit-failure'", "IGNORE"} {
			t.Run(action+mode, func(t *testing.T) {
				cfg := config.Default(t.TempDir()).Database
				db, err := database.Open(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
					t.Fatal(err)
				}
				if _, err := db.DB.Exec(`INSERT INTO users (id, username, email, password_hash, role, status) VALUES ('root', 'root', '', 'test-only', 'super_admin', 'active')`); err != nil {
					t.Fatal(err)
				}
				repo := NewSQLProjectMemberRepository(db.DB, cfg.Driver)
				item, err := repo.CreateManagedProject(t.Context(), "root", "保留原名", "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.DB.Exec(`CREATE TRIGGER reject_project_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(` + mode + `); END`); err != nil {
					t.Fatal(err)
				}
				switch action {
				case "create":
					_, err = repo.CreateManagedProject(t.Context(), "root", "不能保存", "")
				case "update":
					err = repo.UpdateManagedProject(t.Context(), "root", item.ID, "不能保存", "")
				case "delete":
					err = repo.DeleteManagedProject(t.Context(), "root", item.ID)
				}
				if err == nil {
					t.Fatal("审计故障后仍然成功")
				}
				items, err := NewSQLProjectRepository(db.DB, cfg.Driver).ListProjects(t.Context())
				if err != nil || len(items) != 2 {
					t.Fatalf("事务没有回滚：%+v，%v", items, err)
				}
				loaded, err := NewSQLProjectRepository(db.DB, cfg.Driver).GetProject(t.Context(), item.ID)
				if err != nil || loaded.Name != "保留原名" {
					t.Fatalf("资料或删除未回滚：%+v，%v", loaded, err)
				}
			})
		}
	}
}
