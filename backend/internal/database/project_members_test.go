package database

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func prepareProjectMemberMigration(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), migrationTableSQL(driver)); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 13; version++ {
		if err := applyMigration(t.Context(), db, driver, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO projects (id,name,description) VALUES ('member-project','成员项目','保留资料'),('other-project','其他项目','')`,
		`INSERT INTO users (id,username,email,password_hash,role,status) VALUES
			('member-user','member-user','member@example.invalid','test-only','user','active'),
			('other-user','other-user','other@example.invalid','test-only','admin','active')`,
		`INSERT INTO tasks (id,domain_id,task_type,status,message) VALUES ('historical-task','deleted-domain','deploy','success','保留历史')`,
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
}

func assertProjectMemberMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct{ query, want string }{
		{`SELECT description FROM projects WHERE id='member-project'`, "保留资料"},
		{`SELECT message FROM tasks WHERE id='historical-task'`, "保留历史"},
		{`SELECT role FROM users WHERE id='other-user'`, "admin"},
	} {
		var actual string
		if err := db.QueryRowContext(t.Context(), tc.query).Scan(&actual); err != nil || actual != tc.want {
			t.Fatalf("迁移改变历史数据：%q，%v", actual, err)
		}
	}
}

func TestSQLiteProjectMembersMigration(t *testing.T) {
	for _, foreignKeys := range []int{0, 1} {
		for _, recursive := range []int{0, 1} {
			t.Run(fmt.Sprintf("foreign_keys_%d_recursive_%d", foreignKeys, recursive), func(t *testing.T) {
				db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				db.DB.SetMaxOpenConns(1)
				prepareProjectMemberMigration(t, db.DB, "sqlite")
				for _, query := range []string{fmt.Sprintf("PRAGMA foreign_keys=%d", foreignKeys), fmt.Sprintf("PRAGMA recursive_triggers=%d", recursive)} {
					if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM project_members").Scan(&count); err != nil || count != 0 {
					t.Fatalf("迁移自动授权：%d，%v", count, err)
				}
				if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO project_members(project_id,user_id,role) VALUES ('member-project','member-user','read_only')`); err != nil {
					t.Fatal(err)
				}
				for _, query := range []string{
					`DELETE FROM projects WHERE id='member-project'`,
					`DELETE FROM users WHERE id='member-user'`,
					`UPDATE projects SET id='renamed' WHERE id='member-project'`,
					`UPDATE users SET id='renamed' WHERE id='member-user'`,
					`INSERT OR REPLACE INTO projects(id,name,description) VALUES ('member-project','替换','')`,
					`INSERT OR REPLACE INTO projects(rowid,id,name,description) SELECT rowid,'replacement','替换','' FROM projects WHERE id='member-project'`,
					`UPDATE OR REPLACE projects SET id='member-project' WHERE id='other-project'`,
					`UPDATE OR REPLACE projects SET rowid=(SELECT rowid FROM projects WHERE id='member-project') WHERE id='other-project'`,
					`INSERT OR REPLACE INTO users(id,username,email,password_hash,role) VALUES ('replacement','member-user','new@example.invalid','test-only','user')`,
					`INSERT OR REPLACE INTO users(id,username,email,password_hash,role) VALUES ('replacement','new-user','member@example.invalid','test-only','user')`,
					`INSERT OR REPLACE INTO users(id,username,email,password_hash,role) VALUES ('member-user','new-user','new@example.invalid','test-only','user')`,
					`INSERT OR REPLACE INTO users(rowid,id,username,email,password_hash,role) SELECT rowid,'replacement','new-user','new@example.invalid','test-only','user' FROM users WHERE id='member-user'`,
					`UPDATE OR REPLACE users SET id='member-user' WHERE id='other-user'`,
					`UPDATE OR REPLACE users SET username='member-user' WHERE id='other-user'`,
					`UPDATE OR REPLACE users SET email='member@example.invalid' WHERE id='other-user'`,
					`UPDATE OR REPLACE users SET rowid=(SELECT rowid FROM users WHERE id='member-user') WHERE id='other-user'`,
					`UPDATE project_members SET project_id='missing'`,
					`UPDATE project_members SET user_id='missing'`,
					`UPDATE project_members SET role='super_admin'`,
					`INSERT INTO project_members(project_id,user_id,role) VALUES ('missing','member-user','read_only')`,
					`INSERT INTO project_members(project_id,user_id,role) VALUES ('member-project','missing','read_only')`,
				} {
					t.Run(fmt.Sprintf("reject_%02d", count), func(t *testing.T) {
						if _, err := db.DB.ExecContext(t.Context(), query); err == nil {
							t.Fatalf("危险写入未拒绝：%s", query)
						}
						assertProjectMemberMigration(t, db.DB)
					})
					count++
				}
				for _, query := range []string{
					`UPDATE users SET username='renamed-user', email='renamed@example.invalid',status='disabled' WHERE id='member-user'`,
					`UPDATE projects SET name='更名' WHERE id='member-project'`,
					`UPDATE project_members SET role='audit_viewer'`,
				} {
					if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
				var role string
				if err := db.DB.QueryRowContext(t.Context(), "SELECT role FROM project_members").Scan(&role); err != nil || role != "audit_viewer" {
					t.Fatalf("重试改变角色：%q，%v", role, err)
				}
				for _, query := range []string{`DELETE FROM project_members`, `DELETE FROM users WHERE id='member-user'`, `DELETE FROM projects WHERE id='member-project'`} {
					if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestSQLiteProjectMembersMigrationFailure(t *testing.T) {
	for _, fault := range []string{"version", "trigger_collision"} {
		t.Run(fault, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prepareProjectMemberMigration(t, db.DB, "sqlite")
			query := `CREATE TRIGGER fail_member_version BEFORE INSERT ON schema_migrations WHEN NEW.version=14 BEGIN SELECT RAISE(ABORT,'test-only'); END`
			drop := "DROP TRIGGER fail_member_version"
			if fault == "trigger_collision" {
				query = `CREATE TRIGGER scert_members_users_delete BEFORE DELETE ON users BEGIN SELECT 1; END`
				drop = "DROP TRIGGER scert_members_users_delete"
			}
			if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), db.DB, "sqlite"); err == nil {
				t.Fatal("故障未阻断迁移")
			}
			var count int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name='project_members'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("成员表未回滚：%d，%v", count, err)
			}
			if version, err := currentMigrationVersion(t.Context(), db.DB); err != nil || version != 13 {
				t.Fatalf("错误登记版本：%d，%v", version, err)
			}
			assertProjectMemberMigration(t, db.DB)
			if _, err := db.DB.ExecContext(t.Context(), drop); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
				t.Fatal(err)
			}
			assertProjectMemberMigration(t, db.DB)
		})
	}
}

func TestSQLiteProjectMembersReplacementEdges(t *testing.T) {
	for _, scenario := range []string{"negative_rowid", "empty_email", "custom_unique_update"} {
		t.Run(scenario, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.DB.SetMaxOpenConns(1)
			prepareProjectMemberMigration(t, db.DB, "sqlite")
			if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"PRAGMA foreign_keys=0", "PRAGMA recursive_triggers=0",
				`INSERT INTO project_members(project_id,user_id,role) VALUES ('member-project','member-user','read_only')`} {
				if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "negative_rowid" {
				for _, query := range []string{
					`UPDATE users SET rowid=-1 WHERE id='member-user'`,
					`UPDATE projects SET rowid=-1 WHERE id='member-project'`,
					`INSERT INTO users(id,username,email,password_hash,role) VALUES ('new-user','new-user','new@example.invalid','test-only','user')`,
					`INSERT INTO projects(id,name,description) VALUES ('new-project','正常新增','')`,
				} {
					if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatalf("正常写入被错误阻止：%s，%v", query, err)
					}
				}
				for _, query := range []string{
					`INSERT OR REPLACE INTO users(rowid,id,username,email,password_hash,role) VALUES (-1,'replacement','replacement','replacement@example.invalid','test-only','user')`,
					`INSERT OR REPLACE INTO projects(rowid,id,name,description) VALUES (-1,'replacement','替换','')`,
				} {
					if _, err := db.DB.ExecContext(t.Context(), query); err == nil {
						t.Fatalf("负行号替换未拒绝：%s", query)
					}
				}
			} else if scenario == "empty_email" {
				// 既有邮箱索引只约束非空值；有项目角色的无邮箱账户不能阻止新增其他无邮箱账户。
				for _, query := range []string{
					`UPDATE users SET email='' WHERE id='member-user'`,
					`INSERT INTO users(id,username,email,password_hash,role) VALUES ('new-user','new-user','','test-only','user')`,
					`UPDATE users SET email='' WHERE id='other-user'`,
				} {
					if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatalf("正常无邮箱账户写入被拒绝：%s，%v", query, err)
					}
				}
			} else {
				for _, query := range []string{
					`ALTER TABLE users ADD COLUMN extra_tag TEXT`,
					`CREATE UNIQUE INDEX idx_users_extra_tag ON users(extra_tag)`,
					`UPDATE users SET extra_tag='held' WHERE id='member-user'`,
				} {
					if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.DB.ExecContext(t.Context(), `UPDATE OR REPLACE users SET extra_tag='held' WHERE id='other-user'`); err == nil {
					t.Fatal("扩展唯一索引替换绕过了项目成员引用保护")
				}
			}
			assertProjectMemberMigration(t, db.DB)
			var username string
			if err := db.DB.QueryRowContext(t.Context(), "SELECT username FROM users WHERE id='member-user'").Scan(&username); err != nil || username != "member-user" {
				t.Fatalf("关联用户被替换或丢失：%q，%v", username, err)
			}
		})
	}
}

func TestMySQLRealProjectMembersMigrationRetry(t *testing.T) {
	fixture := startRealMySQL(t)
	db := fixture.database(t)
	ctx := t.Context()
	prepareProjectMemberMigration(t, db.DB, "mysql")
	var schema string
	if err := db.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
	// 特意改变当前库默认字符集，成员外键仍必须匹配历史 users.id 的实际类型。
	if _, err := fixture.admin.ExecContext(ctx, "ALTER DATABASE "+schema+" CHARACTER SET latin1 COLLATE latin1_bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.admin.ExecContext(ctx, `CREATE TRIGGER `+schema+`.fail_member_version BEFORE INSERT ON `+schema+`.schema_migrations
		FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test-only'`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.DB, "mysql"); err == nil {
		t.Fatal("版本登记失败未阻断迁移")
	}
	if version, err := currentMigrationVersion(ctx, db.DB); err != nil || version != 13 {
		t.Fatalf("错误登记版本：%d，%v", version, err)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO project_members(project_id,user_id,role) VALUES ('member-project','member-user','dns_admin')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.admin.ExecContext(ctx, "DROP TRIGGER "+schema+".fail_member_version"); err != nil {
		t.Fatal(err)
	}
	// 残缺同名表不能被 IF NOT EXISTS 静默接受；恢复约束后才允许重试。
	if _, err := db.DB.ExecContext(ctx, "ALTER TABLE project_members DROP FOREIGN KEY fk_project_members_user"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.DB, "mysql"); err == nil {
		t.Fatal("残缺成员表被当作迁移成功")
	}
	if version, err := currentMigrationVersion(ctx, db.DB); err != nil || version != 13 {
		t.Fatalf("残缺结构错误登记版本：%d，%v", version, err)
	}
	if _, err := db.DB.ExecContext(ctx, "ALTER TABLE project_members ADD CONSTRAINT fk_project_members_user FOREIGN KEY (user_id) REFERENCES users(id)"); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		if err := Migrate(ctx, db.DB, "mysql"); err != nil {
			t.Fatal(err)
		}
	}
	var role string
	if err := db.DB.QueryRowContext(ctx, "SELECT role FROM project_members").Scan(&role); err != nil || role != "dns_admin" {
		t.Fatalf("重试覆盖成员：%q，%v", role, err)
	}
	assertProjectMemberMigration(t, db.DB)
}
