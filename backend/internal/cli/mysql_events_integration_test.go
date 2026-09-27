package cli

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealNativeEvents(t *testing.T) {
	server := mysqltest.New(t)
	if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = OFF"); err != nil {
		t.Fatal(err)
	}
	source := server.Database(t)
	parsed, err := mysql.ParseDSN(source.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLProgramAccess(t, server, parsed.User, parsed.DBName)
	db, err := database.Open(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{
		"CREATE TABLE event_values (id INT PRIMARY KEY, value INT NOT NULL) ENGINE=InnoDB",
		"INSERT INTO event_values VALUES (1, 10)",
		"CREATE PROCEDURE event_action() UPDATE event_values SET value = value + 7 WHERE id = 1",
		"SET SESSION time_zone = '+08:00'",
		"SET SESSION sql_mode = 'STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION'",
		"CREATE EVENT `中文``事件` ON SCHEDULE AT '2036-01-02 03:04:05' ON COMPLETION PRESERVE DISABLE COMMENT '说明;;不要改写 DISABLE' DO BEGIN CALL event_action(); UPDATE event_values SET value = value + 3 WHERE id = 1; END",
		"SET SESSION time_zone = '-05:00'",
		"CREATE EVENT repeating_event ON SCHEDULE EVERY 2 DAY STARTS '2036-02-03 04:05:06' ENDS '2037-03-04 05:06:07' ON COMPLETION PRESERVE ENABLE COMMENT '周期事件' DO CALL event_action()",
		"CREATE EVENT replica_event ON SCHEDULE EVERY 3 HOUR STARTS '2036-04-05 06:07:08' ON COMPLETION NOT PRESERVE DISABLE ON SLAVE DO CALL event_action()",
		"ALTER DATABASE COLLATE latin1_swedish_ci",
		"SET NAMES latin1 COLLATE latin1_bin",
		"CREATE EVENT `legacy_caf" + string([]byte{0xe9}) + "` ON SCHEDULE EVERY 1 DAY DISABLE COMMENT 'caf" + string([]byte{0xe9}) + ";;' DO SELECT 'caf" + string([]byte{0xe9}) + ";;'",
		"SET NAMES utf8mb4 COLLATE utf8mb4_bin",
		"ALTER DATABASE COLLATE utf8mb4_unicode_ci",
	} {
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	definition := readMySQLEventDefinitions(t, db.DB)
	t.Run("metadata_rejection", func(t *testing.T) {
		tx, err := db.DB.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		original, err := readMySQLStoredDefinition(t.Context(), tx, "EVENT", "legacy_café", "Create Event")
		if err != nil {
			t.Fatal(err)
		}
		body := "SELECT 'café;;'"
		for _, item := range []struct{ name, statement, expected string }{
			{"wrong_body", "CREATE EVENT x DO SELECT 'different'", "正文与目录"},
			{"missing_header", "CREATE EVENT x " + body, "声明头不完整"},
			{"lossy_header", "CREATE EVENT `中文` DO " + body, "无法无损"},
			{"invalid_utf8_header", "CREATE EVENT `ÿ` DO " + body, "无法无损"},
		} {
			t.Run(item.name, func(t *testing.T) {
				definition := make(map[string]string, len(original))
				for key, value := range original {
					definition[key] = value
				}
				definition["Create Event"] = item.statement
				err := normalizeMySQLEventMetadata(t.Context(), tx, "legacy_café", definition)
				if err == nil || !strings.Contains(err.Error(), item.expected) {
					t.Fatalf("无效编码元数据没有明确拒绝：%v", err)
				}
				if definition["Create Event"] != item.statement {
					t.Fatal("失败后留下部分转换定义")
				}
			})
		}
	})
	output := filepath.Join(server.Root(), "native-events.sql")
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
			verify, err := database.Open(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer verify.Close()
			if _, err := verify.DB.ExecContext(t.Context(), "CREATE EVENT `中文``事件` ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1"); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if direction == "scert_to_official" {
					server.Import(t, target, output)
				} else if err := restoreMySQL(t.Context(), target, output); err != nil {
					t.Fatal(err)
				}
				if actual := readMySQLEventDefinitions(t, verify.DB); actual != definition {
					t.Fatalf("事件定义、计划、时区或状态不一致：\n源：%s\n恢复：%s", definition, actual)
				}
				checkMySQLInteropValue(t, target, "SELECT CAST(value AS CHAR) FROM event_values WHERE id = 1", "10")
			}
			// 只在数据、例程、事件全部恢复并核验后，在专用实例显式开启调度。
			if _, err := verify.DB.ExecContext(t.Context(), "ALTER EVENT `中文``事件` ON SCHEDULE AT CURRENT_TIMESTAMP + INTERVAL 2 SECOND ENABLE"); err != nil {
				t.Fatal(err)
			}
			if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = ON"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = OFF"); err != nil {
					t.Error(err)
				}
			}()
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				var value int
				if err := verify.DB.QueryRowContext(t.Context(), "SELECT value FROM event_values WHERE id = 1").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if value == 20 {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			checkMySQLInteropValue(t, target, "SELECT CAST(value AS CHAR) FROM event_values WHERE id = 1", "20")
			checkMySQLInteropValue(t, source, "SELECT CAST(value AS CHAR) FROM event_values WHERE id = 1", "10")
		})
	}
}

func readMySQLEventDefinitions(t *testing.T, db *sql.DB) string {
	t.Helper()
	// 8.0 的 JSON_ARRAY 可能把相同周期文本显示成不透明 Base64。
	// 同时比较显式文本、原始字节、字符集及排序规则，不忽略实际周期值差异。
	rows, err := db.QueryContext(t.Context(), `SELECT JSON_ARRAY(EVENT_NAME, DEFINER, TIME_ZONE,
	 EVENT_BODY, EVENT_DEFINITION, EVENT_TYPE, EXECUTE_AT, CONVERT(INTERVAL_VALUE USING utf8mb4),
	 HEX(INTERVAL_VALUE), CHARSET(INTERVAL_VALUE), COLLATION(INTERVAL_VALUE), INTERVAL_FIELD,
	 STARTS, ENDS, SQL_MODE, STATUS, ON_COMPLETION, EVENT_COMMENT,
	 CHARACTER_SET_CLIENT, COLLATION_CONNECTION, DATABASE_COLLATION)
	 FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() ORDER BY EVENT_NAME`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var definitions []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(definitions, "\n")
}

func TestMySQLRealEventVisibility(t *testing.T) {
	server := mysqltest.New(t)
	scenarios := []string{"visible", "hidden", "empty_without_permission", "global", "unrelated_schema", "escaped_schema", "shadowed_schema", "shadowed_exact"}
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
			if _, err := server.Admin().ExecContext(t.Context(), "GRANT EVENT ON "+quoteMySQLIdentifier(parsed.DBName)+".* TO "+account); err != nil {
				t.Fatal(err)
			}
			if scenario != "empty_without_permission" {
				if _, err := server.Admin().ExecContext(t.Context(), "CREATE EVENT "+quoteMySQLIdentifier(parsed.DBName)+".hidden_event ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "visible" {
				if _, err := server.Admin().ExecContext(t.Context(), "REVOKE EVENT ON "+quoteMySQLIdentifier(parsed.DBName)+".* FROM "+account); err != nil {
					t.Fatal(err)
				}
			}
			schema := quoteMySQLIdentifier(parsed.DBName)
			var grants []string
			switch scenario {
			case "global":
				grants = append(grants, "GRANT EVENT ON *.* TO "+account)
			case "unrelated_schema":
				grants = append(grants, "GRANT EVENT ON `unrelated`.* TO "+account)
			case "escaped_schema":
				escaped := strings.ReplaceAll(parsed.DBName, "_", string(rune(92))+"_")
				grants = append(grants, "GRANT SELECT, EVENT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "shadowed_schema":
				grants = append(grants, "GRANT SELECT ON *.* TO "+account, "GRANT EVENT ON `scert%`.* TO "+account)
			case "shadowed_exact":
				escaped := strings.ReplaceAll(parsed.DBName, "_", string(rune(92))+"_")
				grants = append(grants, "GRANT EVENT ON "+schema+".* TO "+account, "GRANT SELECT ON "+quoteMySQLIdentifier(escaped)+".* TO "+account)
			case "active_role", "nested_role", "inactive_role":
				role := quoteMySQLIdentifier("event_"+scenario) + "@'%'"
				grants = append(grants, "CREATE ROLE "+role, "GRANT EVENT ON "+schema+".* TO "+role, "GRANT "+role+" TO "+account)
				if scenario == "active_role" {
					grants = append(grants, "SET DEFAULT ROLE "+role+" TO "+account)
				}
				if scenario == "nested_role" {
					parent := "'event_parent'@'%'"
					grants = append(grants, "REVOKE "+role+" FROM "+account, "CREATE ROLE "+parent, "GRANT "+role+" TO "+parent, "GRANT "+parent+" TO "+account, "SET DEFAULT ROLE "+parent+" TO "+account)
				}
			case "partial_revoke", "partial_revoke_with_schema":
				grants = append(grants, "SET GLOBAL partial_revokes = ON", "GRANT EVENT ON *.* TO "+account, "REVOKE EVENT ON "+schema+".* FROM "+account)
				if scenario == "partial_revoke_with_schema" {
					grants = append(grants, "GRANT EVENT ON "+schema+".* TO "+account)
				}
			}
			for _, grant := range grants {
				if _, err := server.Admin().ExecContext(t.Context(), grant); err != nil {
					t.Fatal(err)
				}
			}
			allowed := scenario == "visible" || scenario == "global" || scenario == "escaped_schema" || scenario == "active_role" || scenario == "nested_role" || scenario == "partial_revoke_with_schema"
			count := "0"
			if allowed {
				count = "1"
			}
			checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE()", count)
			output := filepath.Join(server.Root(), scenario+"-events.sql")
			err = backupMySQLDatabase(t.Context(), cfg, output)
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(output)
				if err != nil || !strings.Contains(string(data), "hidden_event") {
					t.Fatal("可见事件未导出")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "EVENT") {
				t.Fatalf("缺少事件可见权限必须拒绝，不能生成不完整备份：%v", err)
			}
			for _, path := range []string{output, output + ".tmp"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("权限失败后留下了备份文件：%s", path)
				}
			}
		})
	}
}
