package database

import (
	"strings"
	"testing"
)

func TestMySQLRealResourceProjectMigrationRetry(t *testing.T) {
	fixture := startRealMySQL(t)
	db := fixture.database(t)
	ctx := t.Context()
	if _, err := db.DB.ExecContext(ctx, migrationTableSQL("mysql")); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 10; version++ {
		if err := applyMigration(ctx, db.DB, "mysql", version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`ALTER TABLE domains CONVERT TO CHARACTER SET latin1 COLLATE latin1_bin`,
		`ALTER TABLE servers CONVERT TO CHARACTER SET latin1 COLLATE latin1_bin`,
		`ALTER TABLE domain_servers CONVERT TO CHARACTER SET latin1 COLLATE latin1_bin`,
		`INSERT INTO domains (id, name, certificate_type, sans_json, status) VALUES ('domain', 'example.test', 'single', '[]', 'pending')`,
		`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json, validation_command_json, ssh_key_error) VALUES ('server', 'server', '192.0.2.10', 22, 'deploy', 'secret://test', 'test-only', '/test/cert.pem', '/test/key.pem', '/test/chain.pem', 'nginx', '[]', '[]', '')`,
		`INSERT INTO domain_servers (domain_id, server_id) VALUES ('domain', 'server')`,
		`INSERT INTO domain_servers (domain_id, server_id) VALUES ('missing', 'server')`,
	} {
		if _, err := db.DB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, db.DB, "mysql"); err == nil {
		t.Fatal("历史悬空关联未阻断迁移")
	}
	var count int
	if err := db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM domain_servers").Scan(&count); err != nil || count != 2 {
		t.Fatalf("迁移失败擅自删除历史关联：%d，%v", count, err)
	}
	if _, err := db.DB.ExecContext(ctx, "DELETE FROM domain_servers WHERE domain_id = 'missing'"); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
	if _, err := fixture.admin.ExecContext(ctx, `CREATE TRIGGER `+schema+`.fail_resource_version BEFORE INSERT ON `+schema+`.schema_migrations
		FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'test-only'`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.DB, "mysql"); err == nil {
		t.Fatal("版本登记故障未阻断迁移")
	}
	version, err := currentMigrationVersion(ctx, db.DB)
	if err != nil || version != 10 {
		t.Fatalf("部分执行的迁移错误登记版本：%d，%v", version, err)
	}
	if _, err := db.DB.ExecContext(ctx, `UPDATE domains SET owner_id = 'after-partial', created_by = 'retained', updated_by = 'retained' WHERE id = 'domain'`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.admin.ExecContext(ctx, "DROP TRIGGER "+schema+".fail_resource_version"); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		if err := Migrate(ctx, db.DB, "mysql"); err != nil {
			t.Fatal(err)
		}
	}
	var project, owner, creator, updater string
	if err := db.DB.QueryRowContext(ctx, "SELECT project_id, owner_id, created_by, updated_by FROM domains WHERE id = 'domain'").Scan(&project, &owner, &creator, &updater); err != nil {
		t.Fatal(err)
	}
	if project != "default" || owner != "after-partial" || creator != "retained" || updater != "retained" {
		t.Fatal("重试改写已保存的资源归属")
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND constraint_name IN ('fk_domains_project', 'fk_servers_project', 'fk_binding_domain_project', 'fk_binding_server_project')`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("项目关联外键未完整建立：%d，%v", count, err)
	}
	for _, statement := range []string{
		`DELETE FROM projects WHERE id = 'default'`,
		`UPDATE domains SET project_id = 'DEFAULT' WHERE id = 'domain'`,
		`UPDATE domain_servers SET server_id = 'missing'`,
	} {
		if _, err := db.DB.ExecContext(ctx, statement); err == nil {
			t.Errorf("约束未生效：%s", statement)
		}
	}
	if err := db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM domain_servers WHERE domain_id = 'domain' AND server_id = 'server' AND project_id = 'default'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("历史有效绑定丢失：%d，%v", count, err)
	}
}
