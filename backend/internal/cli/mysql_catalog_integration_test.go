package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealCatalogVisibility(t *testing.T) {
	server := mysqltest.New(t)
	scenarios := []string{"schema", "global", "partial_table", "column_only", "empty_without_permission", "hidden_view", "escaped_schema", "shadowed_schema", "shadowed_exact", "unrelated_schema"}
	if server.IsMySQL8() {
		scenarios = append(scenarios, "active_role", "nested_role", "inactive_role", "partial_revoke", "partial_revoke_with_schema")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			cfg := server.Database(t)
			parsed, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				t.Fatal(err)
			}
			account := mysqlStringLiteral(parsed.User) + "@'127.0.0.1'"
			schema := quoteMySQLIdentifier(parsed.DBName)
			statements := []string{"REVOKE ALL PRIVILEGES ON " + schema + ".* FROM " + account}
			if scenario != "empty_without_permission" {
				statements = append(statements,
					"CREATE TABLE "+schema+".visible_table (id INT, label VARCHAR(20)) ENGINE=InnoDB",
					"CREATE TABLE "+schema+".hidden_table (id INT) ENGINE=InnoDB",
					"INSERT INTO "+schema+".visible_table VALUES (1, '保留值')",
					"INSERT INTO "+schema+".hidden_table VALUES (2)")
			}
			if scenario == "hidden_view" {
				statements = append(statements, "CREATE VIEW "+schema+".hidden_view AS SELECT id FROM "+schema+".hidden_table")
			}
			switch scenario {
			case "empty_without_permission":
				statements = append(statements, "GRANT CREATE ON "+schema+".* TO "+account)
			case "schema":
				statements = append(statements, "GRANT SELECT, TRIGGER, EVENT ON "+schema+".* TO "+account)
			case "global":
				statements = append(statements, "GRANT SELECT ON *.* TO "+account, "GRANT TRIGGER, EVENT ON "+schema+".* TO "+account)
			case "partial_table", "hidden_view":
				statements = append(statements, "GRANT SELECT ON "+schema+".visible_table TO "+account)
			case "column_only":
				statements = append(statements, "GRANT SELECT (id) ON "+schema+".visible_table TO "+account)
			case "escaped_schema":
				escaped := strings.ReplaceAll(parsed.DBName, "_", string(rune(92))+"_")
				statements = append(statements, "GRANT SELECT, TRIGGER, EVENT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "shadowed_schema":
				statements = append(statements, "GRANT SELECT ON `scert%`.* TO "+account, "GRANT INSERT ON "+schema+".* TO "+account)
			case "shadowed_exact":
				escaped := strings.ReplaceAll(parsed.DBName, "_", string(rune(92))+"_")
				statements = append(statements, "GRANT SELECT ON "+schema+".* TO "+account, "GRANT INSERT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "unrelated_schema":
				statements = append(statements, "GRANT CREATE ON "+schema+".* TO "+account, "GRANT SELECT ON `unrelated_database`.* TO "+account)
			case "active_role", "nested_role", "inactive_role":
				role := quoteMySQLIdentifier("catalog_"+scenario) + "@'%'"
				statements = append(statements, "CREATE ROLE "+role, "GRANT SELECT, TRIGGER, EVENT ON "+schema+".* TO "+role, "GRANT "+role+" TO "+account)
				if scenario == "active_role" {
					statements = append(statements, "SET DEFAULT ROLE "+role+" TO "+account)
				}
				if scenario == "inactive_role" {
					statements = append(statements, "GRANT CREATE ON "+schema+".* TO "+account)
				}
				if scenario == "nested_role" {
					parent := "'catalog_parent'@'%'"
					statements = append(statements, "REVOKE "+role+" FROM "+account, "CREATE ROLE "+parent, "GRANT "+role+" TO "+parent, "GRANT "+parent+" TO "+account, "SET DEFAULT ROLE "+parent+" TO "+account)
				}
			case "partial_revoke", "partial_revoke_with_schema":
				statements = append(statements, "SET GLOBAL partial_revokes = ON", "GRANT SELECT ON *.* TO "+account, "REVOKE SELECT ON "+schema+".* FROM "+account, "GRANT TRIGGER, EVENT ON "+schema+".* TO "+account)
				if scenario == "partial_revoke_with_schema" {
					statements = append(statements, "GRANT SELECT ON "+schema+".* TO "+account)
				}
			}
			for _, statement := range statements {
				if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(server.Root(), "catalog-"+scenario+".sql")
			if err := os.WriteFile(output, []byte("existing backup"), 0o600); err != nil {
				t.Fatal(err)
			}
			err = backupMySQLDatabase(t.Context(), cfg, output)
			allowed := scenario == "schema" || scenario == "global" || scenario == "escaped_schema" || scenario == "active_role" || scenario == "nested_role" || scenario == "partial_revoke_with_schema"
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(output)
				if err != nil || !strings.Contains(string(data), "INSERT INTO `visible_table`") || !strings.Contains(string(data), "INSERT INTO `hidden_table`") {
					t.Fatal("已证明完整读取权限，但未导出全部基础表数据")
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "完整表和视图清单") || !strings.Contains(err.Error(), "SELECT") {
					t.Fatalf("无法证明清单完整时必须拒绝，不能遗漏隐藏对象：%v", err)
				}
				assertMySQLBackupFile(t, output, []byte("existing backup"))
			}
			assertMySQLBackupAbsent(t, output+".tmp")
		})
	}
}
