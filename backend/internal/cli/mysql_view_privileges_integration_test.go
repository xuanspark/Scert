package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealViewVisibility(t *testing.T) {
	server := mysqltest.New(t)
	scenarios := []string{"schema", "global", "all_tables", "one_table", "none", "escaped_schema", "shadowed_schema", "shadowed_exact"}
	if server.IsMySQL8() {
		scenarios = append(scenarios, "active_role", "nested_role", "inactive_role", "partial_revoke", "partial_revoke_with_table")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			cfg := server.Database(t)
			dsn, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				t.Fatal(err)
			}
			schema := quoteMySQLIdentifier(dsn.DBName)
			account := mysqlStringLiteral(dsn.User) + "@'127.0.0.1'"
			statements := []string{
				"CREATE VIEW " + schema + ".a AS SELECT 1 AS id",
				"CREATE VIEW " + schema + ".b AS SELECT 2 AS id",
			}
			switch scenario {
			case "schema":
				statements = append(statements, "GRANT SHOW VIEW ON "+schema+".* TO "+account)
			case "global":
				statements = append(statements, "GRANT SHOW VIEW ON *.* TO "+account)
			case "all_tables", "one_table":
				statements = append(statements, "GRANT SHOW VIEW ON "+schema+".a TO "+account)
				if scenario == "all_tables" {
					statements = append(statements, "GRANT SHOW VIEW ON "+schema+".b TO "+account)
				}
			case "escaped_schema":
				escaped := strings.ReplaceAll(dsn.DBName, "_", string(rune(92))+"_")
				statements = append(statements, "GRANT SELECT, TRIGGER, EVENT, SHOW VIEW ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "shadowed_schema":
				statements = append(statements, "GRANT SELECT ON *.* TO "+account, "GRANT SHOW VIEW ON `scert%`.* TO "+account)
			case "shadowed_exact":
				escaped := strings.ReplaceAll(dsn.DBName, "_", string(rune(92))+"_")
				statements = append(statements, "GRANT SHOW VIEW ON "+schema+".* TO "+account, "GRANT SELECT, TRIGGER, EVENT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "active_role", "nested_role", "inactive_role":
				role := quoteMySQLIdentifier("view_"+scenario) + "@'%'"
				statements = append(statements, "CREATE ROLE "+role, "GRANT SHOW VIEW ON "+schema+".* TO "+role, "GRANT "+role+" TO "+account)
				if scenario == "active_role" {
					statements = append(statements, "SET DEFAULT ROLE "+role+" TO "+account)
				}
				if scenario == "nested_role" {
					parent := "'view_parent'@'%'"
					statements = append(statements, "REVOKE "+role+" FROM "+account, "CREATE ROLE "+parent, "GRANT "+role+" TO "+parent, "GRANT "+parent+" TO "+account, "SET DEFAULT ROLE "+parent+" TO "+account)
				}
			case "partial_revoke", "partial_revoke_with_table":
				statements = append(statements, "SET GLOBAL partial_revokes = ON", "GRANT SHOW VIEW ON *.* TO "+account, "REVOKE SHOW VIEW ON "+schema+".* FROM "+account)
				if scenario == "partial_revoke_with_table" {
					statements = append(statements, "GRANT SHOW VIEW ON "+schema+".a TO "+account, "GRANT SHOW VIEW ON "+schema+".b TO "+account)
				}
			}
			for _, statement := range statements {
				if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(server.Root(), "view-visibility-"+scenario+".sql")
			original := []byte("保留原备份")
			writeMySQLBackupFixture(t, file, original)
			err = backupMySQLDatabase(t.Context(), cfg, file)
			allowed := scenario == "schema" || scenario == "global" || scenario == "all_tables" || scenario == "escaped_schema" || scenario == "active_role" || scenario == "nested_role" || scenario == "partial_revoke_with_table"
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "SHOW VIEW") {
					t.Fatalf("视图定义权限缺失时未明确拒绝：%v", err)
				}
				assertMySQLBackupFile(t, file, original)
			}
			assertMySQLBackupAbsent(t, file+".tmp")
		})
	}
}
