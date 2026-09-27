package cli

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

// 官方导出文件不做改写；用同一隔离账户跨测试库恢复，保留原有定义者。
func TestMySQLRealOfficialStoredPrograms(t *testing.T) {
	server := mysqltest.New(t)
	// 仅启用本测试新建实例的事件调度，源事件保持禁用，绝不连接共享服务。
	if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = ON"); err != nil {
		t.Fatal(err)
	}
	source := server.Database(t)
	parsed, err := mysql.ParseDSN(source.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLProgramAccess(t, server, parsed.User, parsed.DBName)
	// 独立实例启用二进制日志时，函数创建需要管理权限；不修改全局信任开关。
	if server.IsMySQL8() {
		if _, err := server.Admin().ExecContext(t.Context(), "GRANT SUPER ON *.* TO "+mysqlStringLiteral(parsed.User)+"@'127.0.0.1'"); err != nil {
			t.Fatal(err)
		}
	}
	if !server.IsMySQL8() {
		if _, err := server.Admin().ExecContext(t.Context(), "GRANT SELECT ON mysql.proc TO "+mysqlStringLiteral(parsed.User)+"@'127.0.0.1'"); err != nil {
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
	statements := []string{
		"SET SESSION sql_mode = 'STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION'",
		"CREATE TABLE interop (id INT PRIMARY KEY, value INT NOT NULL, label VARCHAR(100)) ENGINE=InnoDB",
		"INSERT INTO interop VALUES (1, 10, '原始值')",
		"CREATE TRIGGER first_trigger BEFORE INSERT ON interop FOR EACH ROW BEGIN SET NEW.value = NEW.value + 2; SET NEW.label = CONCAT(NEW.label, ';第一步'); END",
		"CREATE TRIGGER second_trigger BEFORE INSERT ON interop FOR EACH ROW FOLLOWS first_trigger BEGIN SET NEW.value = NEW.value * 3; SET NEW.label = CONCAT(NEW.label, ';第二步'); END",
		"CREATE PROCEDURE change_value(IN target_id INT) BEGIN UPDATE interop SET value = value + 5 WHERE id = target_id; UPDATE interop SET label = CONCAT(label, ';过程') WHERE id = target_id; END",
		"CREATE FUNCTION format_value(input_value INT) RETURNS VARCHAR(100) DETERMINISTIC NO SQL BEGIN DECLARE result VARCHAR(100); SET result = CONCAT('函数;', input_value); RETURN result; END",
		"CREATE EVENT disabled_event ON SCHEDULE AT '2036-01-01 00:00:00' ON COMPLETION PRESERVE DISABLE DO BEGIN UPDATE interop SET value = value + 100; UPDATE interop SET label = '事件;执行'; END",
	}
	for _, statement := range statements {
		if _, err := connection.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	definition := readMySQLProgramDefinitions(t, db.DB)
	output := filepath.Join(server.Root(), "official-programs.sql")
	server.DumpStoredPrograms(t, source, output)
	for _, direction := range []string{"official_control", "official_to_scert"} {
		t.Run(direction, func(t *testing.T) {
			target := server.Database(t)
			targetDSN, err := mysql.ParseDSN(target.DSN)
			if err != nil {
				t.Fatal(err)
			}
			grantMySQLProgramAccess(t, server, parsed.User, targetDSN.DBName)
			targetDSN.User = parsed.User
			target.DSN = targetDSN.FormatDSN()
			if direction == "official_control" {
				server.Import(t, target, output)
			} else if err := restoreMySQL(t.Context(), target, output); err != nil {
				t.Fatal(err)
			}
			checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 1", "10|原始值")
			verify, err := database.Open(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer verify.Close()
			if restored := readMySQLProgramDefinitions(t, verify.DB); restored != definition {
				t.Fatalf("存储对象的定义、执行顺序、权限上下文或事件状态发生变化：\n源：%s\n恢复：%s", definition, restored)
			}
			for _, statement := range []string{"INSERT INTO interop VALUES (2, 4, '新行')", "CALL change_value(1)"} {
				if _, err := verify.DB.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 2", "18|新行;第一步;第二步")
			checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 1", "15|原始值;过程")
			checkMySQLInteropValue(t, target, "SELECT format_value(7)", "函数;7")
			if _, err := verify.DB.ExecContext(t.Context(), "ALTER EVENT disabled_event ON SCHEDULE AT CURRENT_TIMESTAMP + INTERVAL 2 SECOND ENABLE"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			var value int
			for time.Now().Before(deadline) {
				if err := verify.DB.QueryRowContext(t.Context(), "SELECT value FROM interop WHERE id = 1").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if value == 115 {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 1", "115|事件;执行")
			checkMySQLInteropValue(t, source, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 1", "10|原始值")
		})
	}
	t.Run("insufficient_privileges", func(t *testing.T) {
		target := server.Database(t)
		// 通用夹具现有 TRIGGER 可见权限；本失败场景显式撤销，继续覆盖无对象管理权限的恢复。
		limited, err := mysql.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.Admin().ExecContext(t.Context(), "REVOKE TRIGGER ON "+quoteMySQLIdentifier(limited.DBName)+".* FROM "+mysqlStringLiteral(limited.User)+"@'127.0.0.1'"); err != nil {
			t.Fatal(err)
		}
		err = restoreMySQL(t.Context(), target, output)
		var serverError *mysql.MySQLError
		if !errors.As(err, &serverError) || (serverError.Number != 1142 && serverError.Number != 1227 && serverError.Number != 1419) {
			t.Fatalf("权限不足时必须返回原始数据库错误，不能忽略或提升权限：%v", err)
		}
		parsedTarget, err := mysql.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := server.Admin().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ?", parsedTarget.DBName).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("触发器恢复失败后仍继续恢复了后面的例程")
		}
	})
	t.Run("malformed_script_is_not_executed", func(t *testing.T) {
		target := server.Database(t)
		invalid := filepath.Join(server.Root(), "malformed-programs.sql")
		if err := os.WriteFile(invalid, []byte("CREATE TABLE should_not_exist (id INT);\nDELIMITER\nSELECT 1;"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := restoreMySQL(t.Context(), target, invalid); err == nil {
			t.Fatal("无效分隔符没有阻止恢复")
		}
		checkMySQLInteropValue(t, target, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()", "0")
	})
}

func grantMySQLProgramAccess(t *testing.T, server *mysqltest.Server, user, name string) {
	t.Helper()
	statement := "GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, DROP, REFERENCES, TRIGGER, CREATE ROUTINE, ALTER ROUTINE, EXECUTE, EVENT ON " + quoteMySQLIdentifier(name) + ".* TO " + mysqlStringLiteral(user) + "@'127.0.0.1'"
	if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func readMySQLProgramDefinitions(t *testing.T, db *sql.DB) string {
	t.Helper()
	queries := []string{
		`SELECT JSON_ARRAY(TRIGGER_NAME, EVENT_MANIPULATION, EVENT_OBJECT_TABLE, ACTION_ORDER,
		 ACTION_STATEMENT, ACTION_ORIENTATION, ACTION_TIMING, SQL_MODE, DEFINER,
		 CHARACTER_SET_CLIENT, COLLATION_CONNECTION, DATABASE_COLLATION)
		 FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME`,
		`SELECT JSON_ARRAY(ROUTINE_NAME, ROUTINE_TYPE, DATA_TYPE, DTD_IDENTIFIER, ROUTINE_DEFINITION,
		 IS_DETERMINISTIC, SQL_DATA_ACCESS, SECURITY_TYPE, SQL_MODE, DEFINER, ROUTINE_COMMENT,
		 CHARACTER_SET_CLIENT, COLLATION_CONNECTION, DATABASE_COLLATION)
		 FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() ORDER BY ROUTINE_NAME`,
		`SELECT JSON_ARRAY(SPECIFIC_NAME, ORDINAL_POSITION, PARAMETER_MODE, PARAMETER_NAME,
		 DATA_TYPE, DTD_IDENTIFIER, CHARACTER_SET_NAME, COLLATION_NAME, ROUTINE_TYPE)
		 FROM information_schema.PARAMETERS WHERE SPECIFIC_SCHEMA = DATABASE()
		 ORDER BY SPECIFIC_NAME, ORDINAL_POSITION`,
		`SELECT JSON_ARRAY(EVENT_NAME, DEFINER, TIME_ZONE, EVENT_BODY, EVENT_DEFINITION, EVENT_TYPE,
		 EXECUTE_AT, SQL_MODE, STATUS, ON_COMPLETION, EVENT_COMMENT,
		 CHARACTER_SET_CLIENT, COLLATION_CONNECTION, DATABASE_COLLATION)
		 FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() ORDER BY EVENT_NAME`,
	}
	var definitions []string
	for _, query := range queries {
		rows, err := db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
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
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(definitions) != 8 {
		t.Fatalf("存储对象及参数数量不完整：得到 %d，期望 8", len(definitions))
	}
	return strings.Join(definitions, "\n")
}
