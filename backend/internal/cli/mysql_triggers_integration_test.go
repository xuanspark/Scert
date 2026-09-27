package cli

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealNativeTriggers(t *testing.T) {
	server := mysqltest.New(t)
	source := server.Database(t)
	parsed, err := mysql.ParseDSN(source.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLProgramAccess(t, server, parsed.User, parsed.DBName)
	// 仅给独立实例的专用账户补足启用二进制日志时的触发器创建权限。
	if server.IsMySQL8() {
		if _, err := server.Admin().ExecContext(t.Context(), "GRANT SUPER ON *.* TO "+mysqlStringLiteral(parsed.User)+"@'127.0.0.1'"); err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	connection, err := db.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, statement := range []string{
		"SET SESSION sql_mode = 'STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION'",
		"SET NAMES utf8mb4 COLLATE utf8mb4_bin",
		"CREATE TABLE interop (id INT PRIMARY KEY, value INT NOT NULL, label VARCHAR(100)) ENGINE=InnoDB",
		"CREATE TABLE audit_values (value INT NOT NULL) ENGINE=InnoDB",
		"INSERT INTO interop VALUES (1, 10, '原始值')",
		"CREATE TRIGGER z_first BEFORE INSERT ON interop FOR EACH ROW BEGIN SET NEW.value = NEW.value + 2; SET NEW.label = CONCAT(NEW.label, ';第一步'); END",
		"CREATE TRIGGER a_second BEFORE INSERT ON interop FOR EACH ROW FOLLOWS z_first BEGIN SET NEW.value = NEW.value * 3; SET NEW.label = CONCAT(NEW.label, ';第二步'); END",
		"CREATE TRIGGER audit_insert AFTER INSERT ON interop FOR EACH ROW BEGIN INSERT INTO audit_values VALUES (NEW.value); END",
		"CREATE TABLE legacy_values (id INT PRIMARY KEY, label VARCHAR(100)) ENGINE=InnoDB",
		"ALTER DATABASE COLLATE latin1_swedish_ci",
		"SET NAMES latin1 COLLATE latin1_bin",
		"CREATE TRIGGER legacy_insert BEFORE INSERT ON legacy_values FOR EACH ROW SET NEW.label = CONCAT(NEW.label, 'caf" + string([]byte{0xe9}) + ";;')",
		"SET NAMES utf8mb4 COLLATE utf8mb4_bin",
		"ALTER DATABASE COLLATE utf8mb4_unicode_ci",
	} {
		if _, err := connection.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	definition := readMySQLTriggerDefinitions(t, db.DB)
	if strings.Count(definition, "\n") != 3 {
		t.Fatal("源库四个触发器没有完整创建")
	}
	output := filepath.Join(server.Root(), "native-triggers.sql")
	if err := backupMySQLDatabase(t.Context(), source, output); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"scert_to_scert", "scert_to_official"} {
		t.Run(direction, func(t *testing.T) {
			target := server.Database(t)
			targetDSN, err := mysql.ParseDSN(target.DSN)
			if err != nil {
				t.Fatal(err)
			}
			grantMySQLProgramAccess(t, server, parsed.User, targetDSN.DBName)
			targetDSN.User = parsed.User
			target.DSN = targetDSN.FormatDSN()
			if direction == "scert_to_official" {
				server.Import(t, target, output)
			} else if err := restoreMySQL(t.Context(), target, output); err != nil {
				t.Fatal(err)
			}
			verify, err := database.Open(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer verify.Close()
			if restored := readMySQLTriggerDefinitions(t, verify.DB); restored != definition {
				t.Fatalf("触发器定义、顺序或创建上下文发生变化：\n源：%s\n恢复：%s", definition, restored)
			}
			checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 1", "10|原始值")
			checkMySQLInteropValue(t, target, "SELECT CAST(COUNT(*) AS CHAR) FROM audit_values", "0")
			if _, err := verify.DB.ExecContext(t.Context(), "INSERT INTO interop VALUES (2, 4, '新行')"); err != nil {
				t.Fatal(err)
			}
			checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 2", "18|新行;第一步;第二步")
			checkMySQLInteropValue(t, target, "SELECT CAST(value AS CHAR) FROM audit_values", "18")
			if _, err := verify.DB.ExecContext(t.Context(), "INSERT INTO legacy_values VALUES (1, '')"); err != nil {
				t.Fatal(err)
			}
			checkMySQLInteropValue(t, target, "SELECT label FROM legacy_values", "café;;")
			checkMySQLInteropValue(t, target, "SELECT @@collation_database", "utf8mb4_unicode_ci")
		})
	}
	t.Run("hidden_triggers_are_not_silently_lost", func(t *testing.T) {
		if _, err := server.Admin().ExecContext(t.Context(), "REVOKE TRIGGER ON "+quoteMySQLIdentifier(parsed.DBName)+".* FROM "+mysqlStringLiteral(parsed.User)+"@'127.0.0.1'"); err != nil {
			t.Fatal(err)
		}
		checkMySQLInteropValue(t, source, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE()", "0")
		failed := filepath.Join(server.Root(), "hidden-triggers.sql")
		if err := os.WriteFile(failed, []byte("existing backup"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := backupMySQLDatabase(t.Context(), source, failed); err == nil || !strings.Contains(err.Error(), "TRIGGER") {
			t.Fatalf("没有触发器可见权限时必须明确失败：%v", err)
		}
		data, err := os.ReadFile(failed)
		if err != nil || string(data) != "existing backup" {
			t.Fatal("权限不足破坏了已有备份")
		}
		if _, err := os.Stat(failed + ".tmp"); !os.IsNotExist(err) {
			t.Fatal("权限不足遗留临时文件")
		}
	})
}

func TestMySQLRealTriggerPrivileges(t *testing.T) {
	server := mysqltest.New(t)
	scenarios := []string{"schema", "global", "per_table", "partial_table", "unrelated_schema", "escaped_schema", "escaped_identifier", "shadowed_schema", "shadowed_exact"}
	if server.IsMySQL8() {
		scenarios = append(scenarios, "global_dynamic", "active_role", "nested_role", "inactive_role", "partial_revoke")
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
			first, second := "first_table", "second_table"
			if scenario == "escaped_identifier" {
				first, second = "first`table`", "second_table%"
			}
			statements := []string{
				"CREATE TABLE " + schema + "." + quoteMySQLIdentifier(first) + " (id INT) ENGINE=InnoDB",
				"CREATE TABLE " + schema + "." + quoteMySQLIdentifier(second) + " (id INT) ENGINE=InnoDB",
				"REVOKE TRIGGER ON " + schema + ".* FROM " + account,
			}
			switch scenario {
			case "schema":
				statements = append(statements, "GRANT TRIGGER ON "+schema+".* TO "+account)
			case "escaped_schema":
				escaped := strings.ReplaceAll(parsed.DBName, "_", string(rune(92))+"_")
				statements = append(statements, "GRANT SELECT, TRIGGER, EVENT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "shadowed_schema":
				statements = append(statements, "GRANT TRIGGER ON `scert%`.* TO "+account)
			case "shadowed_exact":
				escaped := strings.ReplaceAll(parsed.DBName, "_", string(rune(92))+"_")
				statements = append(statements, "GRANT TRIGGER ON "+schema+".* TO "+account, "GRANT SELECT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "global":
				statements = append(statements, "GRANT TRIGGER ON *.* TO "+account)
			case "global_dynamic":
				statements = append(statements, "GRANT TRIGGER, BACKUP_ADMIN ON *.* TO "+account)
			case "per_table", "escaped_identifier":
				statements = append(statements, "GRANT TRIGGER ON "+schema+"."+quoteMySQLIdentifier(first)+" TO "+account,
					"GRANT TRIGGER ON "+schema+"."+quoteMySQLIdentifier(second)+" TO "+account)
			case "partial_table":
				statements = append(statements, "GRANT TRIGGER ON "+schema+"."+quoteMySQLIdentifier(first)+" TO "+account)
			case "unrelated_schema":
				statements = append(statements, "GRANT TRIGGER ON `another_database`.* TO "+account)
			case "active_role", "nested_role", "inactive_role":
				role := quoteMySQLIdentifier("trigger_role_"+scenario) + "@'%'"
				statements = append(statements, "CREATE ROLE "+role, "GRANT TRIGGER ON "+schema+".* TO "+role, "GRANT "+role+" TO "+account)
				if scenario == "active_role" {
					statements = append(statements, "SET DEFAULT ROLE "+role+" TO "+account)
				}
				if scenario == "nested_role" {
					parent := "'trigger_parent'@'%'"
					statements = append(statements, "REVOKE "+role+" FROM "+account, "CREATE ROLE "+parent, "GRANT "+role+" TO "+parent,
						"GRANT "+parent+" TO "+account, "SET DEFAULT ROLE "+parent+" TO "+account)
				}
			case "partial_revoke":
				statements = append(statements, "SET GLOBAL partial_revokes = ON", "GRANT TRIGGER ON *.* TO "+account, "REVOKE TRIGGER ON "+schema+".* FROM "+account)
			}
			for _, statement := range statements {
				if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			for i, table := range []string{first, second} {
				trigger := quoteMySQLIdentifier("probe_" + string(rune('a'+i)))
				statement := "CREATE TRIGGER " + schema + "." + trigger + " BEFORE INSERT ON " + schema + "." + quoteMySQLIdentifier(table) + " FOR EACH ROW SET NEW.id = NEW.id + 1"
				if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(server.Root(), "privilege-"+scenario+".sql")
			err = backupMySQLDatabase(t.Context(), cfg, output)
			denied := scenario == "partial_table" || scenario == "unrelated_schema" || scenario == "inactive_role" || scenario == "partial_revoke" || scenario == "shadowed_schema" || scenario == "shadowed_exact"
			if denied {
				if err == nil || !strings.Contains(err.Error(), "TRIGGER") {
					t.Fatalf("缺少有效权限时没有明确拒绝备份：%v", err)
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("权限不足仍发布了备份")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			count := "2"
			if denied {
				count = "0"
				if scenario == "partial_table" {
					count = "1"
				}
			}
			checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE()", count)
		})
	}
}

func readMySQLTriggerDefinitions(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT JSON_ARRAY(TRIGGER_NAME, EVENT_MANIPULATION,
	 EVENT_OBJECT_TABLE, ACTION_ORDER, ACTION_STATEMENT, ACTION_ORIENTATION, ACTION_TIMING,
	 SQL_MODE, DEFINER, CHARACTER_SET_CLIENT, COLLATION_CONNECTION, DATABASE_COLLATION)
	 FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var definitions []string
	for rows.Next() {
		var definition string
		if err := rows.Scan(&definition); err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, definition)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(definitions, "\n")
}
