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

func TestMySQLRealNativeRoutines(t *testing.T) {
	server := mysqltest.New(t)
	source := server.Database(t)
	parsed, err := mysql.ParseDSN(source.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLProgramAccess(t, server, parsed.User, parsed.DBName)
	// 只调整独立实例专用账户，不能修改共享服务的函数信任开关。
	account := mysqlStringLiteral(parsed.User) + "@'127.0.0.1'"
	grant := "GRANT SELECT ON mysql.proc TO " + account
	if server.IsMySQL8() {
		grant = "GRANT SHOW_ROUTINE, SUPER ON *.* TO " + account
	}
	if _, err := server.Admin().ExecContext(t.Context(), grant); err != nil {
		t.Fatal(err)
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
		"INSERT INTO interop VALUES (1, 10, '原始值')",
		"CREATE FUNCTION format_value(input_value DECIMAL(20,4)) RETURNS VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci DETERMINISTIC NO SQL SQL SECURITY INVOKER COMMENT '函数说明' RETURN CONCAT('函数;', input_value)",
		"CREATE PROCEDURE `change``value`(IN target_id INT, INOUT step_value INT, OUT result_label VARCHAR(100)) SQL SECURITY INVOKER COMMENT '过程说明' BEGIN UPDATE interop SET value = value + step_value WHERE id = target_id; SET step_value = step_value + 1; SET result_label = format_value(step_value); END",
		"CREATE TRIGGER routine_trigger BEFORE INSERT ON interop FOR EACH ROW SET NEW.label = format_value(NEW.value)",
		"ALTER DATABASE COLLATE latin1_swedish_ci",
		"SET NAMES latin1 COLLATE latin1_bin",
		"CREATE FUNCTION `legacy_caf" + string([]byte{0xe9}) + "`() RETURNS VARCHAR(100) DETERMINISTIC NO SQL COMMENT 'caf" + string([]byte{0xe9}) + ";;' RETURN 'caf" + string([]byte{0xe9}) + ";;'",
		"SET NAMES utf8mb4 COLLATE utf8mb4_bin",
		"ALTER DATABASE COLLATE utf8mb4_unicode_ci",
	} {
		if _, err := connection.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	definition := readMySQLRoutineDefinitions(t, db.DB)
	t.Run("metadata_rejection", func(t *testing.T) {
		checkMySQLRoutineMetadataRejection(t, db.DB, server.IsMySQL8())
	})
	output := filepath.Join(server.Root(), "native-routines.sql")
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
			// 恢复到已有同名例程的数据库，不能只验证空库创建。
			if _, err := verify.DB.ExecContext(t.Context(), "CREATE FUNCTION format_value(input_value INT) RETURNS INT DETERMINISTIC NO SQL RETURN -1"); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if direction == "scert_to_official" {
					server.Import(t, target, output)
				} else if err := restoreMySQL(t.Context(), target, output); err != nil {
					t.Fatal(err)
				}
				if restored := readMySQLRoutineDefinitions(t, verify.DB); restored != definition {
					t.Fatalf("例程定义、参数、权限上下文或字符集不一致：\n源：%s\n恢复：%s", definition, restored)
				}
				checkMySQLInteropValue(t, target, "SELECT CONCAT(value, '|', label) FROM interop WHERE id = 1", "10|原始值")
				checkMySQLInteropValue(t, target, "SELECT format_value(7)", "函数;7.0000")
				checkMySQLInteropValue(t, target, "SELECT `legacy_café`()", "café;;")
				checkMySQLInteropValue(t, target, "SELECT @@collation_database", "utf8mb4_unicode_ci")
				conn, err := verify.DB.Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, statement := range []string{"SET @step = 5", "CALL `change``value`(1, @step, @label)", "INSERT INTO interop VALUES (2, 4, '新行')"} {
					if _, err := conn.ExecContext(t.Context(), statement); err != nil {
						_ = conn.Close()
						t.Fatal(err)
					}
				}
				var actual string
				err = conn.QueryRowContext(t.Context(), "SELECT CONCAT(@step, '|', @label)").Scan(&actual)
				_ = conn.Close()
				if err != nil || actual != "6|函数;6.0000" {
					t.Fatalf("过程输入输出参数错误：%q，%v", actual, err)
				}
				checkMySQLInteropValue(t, target, "SELECT CAST(value AS CHAR) FROM interop WHERE id = 1", "15")
				checkMySQLInteropValue(t, target, "SELECT label FROM interop WHERE id = 2", "函数;4.0000")
			}
		})
	}
}

func TestMySQLRealRoutineVisibility(t *testing.T) {
	server := mysqltest.New(t)
	scenarios := []string{"visible", "hidden", "empty_schema_without_permission", "global_select", "execute_only"}
	if server.IsMySQL8() {
		scenarios = append(scenarios, "active_role", "nested_role", "inactive_role", "partial_revoke", "partial_revoke_with_show")
	} else {
		scenarios = append(scenarios, "column_only")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			cfg := server.Database(t)
			parsed, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				t.Fatal(err)
			}
			account := mysqlStringLiteral(parsed.User) + "@'127.0.0.1'"
			permission := "SELECT ON mysql.proc"
			if server.IsMySQL8() {
				permission = "SHOW_ROUTINE ON *.*"
			}
			if _, err := server.Admin().ExecContext(t.Context(), "GRANT "+permission+" TO "+account); err != nil {
				t.Fatal(err)
			}
			if scenario != "empty_schema_without_permission" {
				statement := "CREATE PROCEDURE " + quoteMySQLIdentifier(parsed.DBName) + ".hidden_routine() SELECT 123"
				if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			allowed := scenario == "visible" || scenario == "global_select" || scenario == "active_role" || scenario == "nested_role" || scenario == "partial_revoke_with_show"
			if scenario != "visible" && scenario != "partial_revoke_with_show" {
				if _, err := server.Admin().ExecContext(t.Context(), "REVOKE "+permission+" FROM "+account); err != nil {
					t.Fatal(err)
				}
			}
			var statements []string
			switch scenario {
			case "global_select":
				statements = append(statements, "GRANT SELECT ON *.* TO "+account)
			case "execute_only":
				statements = append(statements, "GRANT EXECUTE ON "+quoteMySQLIdentifier(parsed.DBName)+".* TO "+account)
			case "column_only":
				statements = append(statements, "GRANT SELECT (db, name, type) ON mysql.proc TO "+account)
			case "active_role", "nested_role", "inactive_role":
				role := quoteMySQLIdentifier("routine_"+scenario) + "@'%'"
				statements = append(statements, "CREATE ROLE "+role, "GRANT SHOW_ROUTINE ON *.* TO "+role, "GRANT "+role+" TO "+account)
				if scenario == "active_role" {
					statements = append(statements, "SET DEFAULT ROLE "+role+" TO "+account)
				}
				if scenario == "nested_role" {
					parent := "'routine_parent'@'%'"
					statements = append(statements, "REVOKE "+role+" FROM "+account, "CREATE ROLE "+parent, "GRANT "+role+" TO "+parent, "GRANT "+parent+" TO "+account, "SET DEFAULT ROLE "+parent+" TO "+account)
				}
			case "partial_revoke", "partial_revoke_with_show":
				// 先移除夹具的库级授权，否则一次 REVOKE 只移除该授权而不撤销全局权限。
				statements = append(statements, "REVOKE SELECT ON "+quoteMySQLIdentifier(parsed.DBName)+".* FROM "+account,
					"SET GLOBAL partial_revokes = ON", "GRANT SELECT ON *.* TO "+account, "REVOKE SELECT ON "+quoteMySQLIdentifier(parsed.DBName)+".* FROM "+account)
			}
			for _, statement := range statements {
				if _, err := server.Admin().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "hidden" || scenario == "empty_schema_without_permission" || scenario == "inactive_role" {
				checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE()", "0")
			}
			if allowed || scenario == "execute_only" {
				checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE()", "1")
			}
			if scenario == "execute_only" {
				checkMySQLInteropValue(t, cfg, "SELECT CAST(ROUTINE_DEFINITION IS NULL AS CHAR) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE()", "1")
			}
			output := filepath.Join(server.Root(), "routines-"+scenario+".sql")
			if err := os.WriteFile(output, []byte("existing backup"), 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario == "partial_revoke" || scenario == "partial_revoke_with_show" {
				db, err := database.Open(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				tx, err := db.DB.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				routines, visibilityErr := listMySQLRoutines(t.Context(), tx)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if allowed {
					if visibilityErr != nil || len(routines) != 1 || routines[0].name != "hidden_routine" {
						t.Fatalf("SHOW_ROUTINE 未独立保证例程可见：%v", visibilityErr)
					}
				} else if visibilityErr == nil || !strings.Contains(visibilityErr.Error(), "例程") {
					t.Fatalf("全局 SELECT 被部分撤销后例程权限检查未拒绝：%v", visibilityErr)
				}
				if err := backupMySQLDatabase(t.Context(), cfg, output); err == nil || !strings.Contains(err.Error(), "SELECT") {
					t.Fatalf("例程可见权限不能替代完整表清单的读取权限：%v", err)
				}
				assertMySQLBackupFile(t, output, []byte("existing backup"))
				assertMySQLBackupAbsent(t, output+".tmp")
				if !allowed {
					return
				}
				if _, err := server.Admin().ExecContext(t.Context(), "GRANT SELECT ON "+quoteMySQLIdentifier(parsed.DBName)+".* TO "+account); err != nil {
					t.Fatal(err)
				}
			}
			err = backupMySQLDatabase(t.Context(), cfg, output)
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(output)
				if err != nil || !strings.Contains(string(data), "hidden_routine") {
					t.Fatal("可见例程未完整导出")
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "例程") {
					t.Fatalf("缺少完整例程可见权限必须拒绝：%v", err)
				}
				data, readErr := os.ReadFile(output)
				if readErr != nil || string(data) != "existing backup" {
					t.Fatal("权限不足破坏了原有备份")
				}
				if _, err := os.Stat(output + ".tmp"); !os.IsNotExist(err) {
					t.Fatal("权限不足遗留临时文件")
				}
			}
		})
	}
}

func checkMySQLRoutineMetadataRejection(t *testing.T, db *sql.DB, mysql8 bool) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	routine := mysqlRoutine{name: "legacy_café", kind: "FUNCTION", utf8MetadataHeader: mysql8}
	const column = "Create Function"
	for _, item := range []struct {
		name    string
		alter   func(string) string
		message string
	}{
		{"incomplete_header", func(string) string { return "CREATE FUNCTION" }, "参数列表"},
		{"body_mismatch", func(value string) string { return value + " " }, "正文与目录元数据不一致"},
		{"missing_comment", func(value string) string {
			return strings.Replace(value, "\n    COMMENT ", "\n    REMARK ", 1)
		}, "注释声明不完整"},
		{"lossy_comment", func(value string) string {
			return strings.Replace(value, "\n    COMMENT '", "\n    COMMENT '中文", 1)
		}, "无法无损还原"},
		{"invalid_utf8_comment", func(value string) string {
			return strings.Replace(value, "\n    COMMENT '", "\n    COMMENT 'é", 1)
		}, "无法无损还原"},
	} {
		t.Run(item.name, func(t *testing.T) {
			definition, err := readMySQLStoredDefinition(t.Context(), tx, routine.kind, routine.name, column)
			if err != nil {
				t.Fatal(err)
			}
			definition[column] = item.alter(definition[column])
			original := definition[column]
			err = normalizeMySQLRoutineMetadata(t.Context(), tx, routine, definition, column)
			if err == nil || !strings.Contains(err.Error(), item.message) {
				t.Fatalf("无效例程元数据必须明确拒绝：%v", err)
			}
			if definition[column] != original {
				t.Fatal("失败时不能写回部分转换的定义")
			}
		})
	}
}

func readMySQLRoutineDefinitions(t *testing.T, db *sql.DB) string {
	t.Helper()
	queries := []string{
		`SELECT JSON_ARRAY(ROUTINE_NAME, ROUTINE_TYPE, DTD_IDENTIFIER, ROUTINE_BODY, ROUTINE_DEFINITION,
		 IS_DETERMINISTIC, SQL_DATA_ACCESS, SECURITY_TYPE, SQL_MODE, ROUTINE_COMMENT, DEFINER,
		 CHARACTER_SET_CLIENT, COLLATION_CONNECTION, DATABASE_COLLATION)
		 FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() ORDER BY ROUTINE_TYPE, ROUTINE_NAME`,
		`SELECT JSON_ARRAY(SPECIFIC_NAME, ORDINAL_POSITION, PARAMETER_MODE, PARAMETER_NAME,
		 DTD_IDENTIFIER, CHARACTER_SET_NAME, COLLATION_NAME, ROUTINE_TYPE)
		 FROM information_schema.PARAMETERS WHERE SPECIFIC_SCHEMA = DATABASE() ORDER BY SPECIFIC_NAME, ORDINAL_POSITION`,
	}
	var definitions []string
	for _, query := range queries {
		rows, err := db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var definition string
			if err := rows.Scan(&definition); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			definitions = append(definitions, definition)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join(definitions, "\n")
}
