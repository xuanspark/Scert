package cli

import (
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealNativeViews(t *testing.T) {
	server := mysqltest.New(t)
	source := server.Database(t)
	parsed, err := mysql.ParseDSN(source.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLViewAccess(t, server, parsed.User, parsed.DBName)
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
		"CREATE TABLE source_values (id INT PRIMARY KEY, amount DECIMAL(25,8), label VARCHAR(80)) ENGINE=InnoDB",
		"INSERT INTO source_values VALUES (1, 12345678901234567.12345678, '原始值'), (2, -1.25000000, '负数')",
		"CREATE FUNCTION view_label(input_value VARCHAR(80)) RETURNS VARCHAR(100) DETERMINISTIC NO SQL RETURN CONCAT('函数;', input_value)",
		"CREATE ALGORITHM=MERGE SQL SECURITY INVOKER VIEW z_base AS SELECT id, amount, label FROM source_values WHERE id > 0 WITH LOCAL CHECK OPTION",
		"CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW a_nested AS SELECT id, amount, label FROM z_base WHERE id = 1 WITH CASCADED CHECK OPTION",
		"CREATE ALGORITHM=TEMPTABLE SQL SECURITY INVOKER VIEW `中文``汇总` AS SELECT SUM(amount) AS `金额`, view_label(MAX(label)) AS `说明` FROM a_nested",
		"SET NAMES latin1 COLLATE latin1_bin",
		"CREATE SQL SECURITY INVOKER VIEW `legacy_caf" + string([]byte{0xe9}) + "` AS SELECT 'caf" + string([]byte{0xe9}) + ";;' AS label",
		"SET NAMES utf8mb4 COLLATE utf8mb4_bin",
	} {
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	original := readMySQLViewDefinitions(t, db.DB)
	output := filepath.Join(server.Root(), "native-views.sql")
	if err := backupMySQLDatabase(t.Context(), source, output); err != nil {
		t.Fatal(err)
	}
	official := filepath.Join(server.Root(), "official-views.sql")
	server.DumpStoredPrograms(t, source, official)
	before, err := os.ReadFile(official)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(before)
	t.Run("official_to_official_control", func(t *testing.T) {
		target := server.Database(t)
		dsn, err := mysql.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		grantMySQLViewAccess(t, server, parsed.User, dsn.DBName)
		dsn.User = parsed.User
		target.DSN = dsn.FormatDSN()
		if code := server.ImportErrorCode(t, target, official); code != 1368 {
			t.Fatalf("官方原样恢复未复现嵌套检查选项错误：%d", code)
		}
		t.Log("同一份未经改写的官方导出文件，官方客户端返回 1368；Scert 必须单独验证成功恢复，不能用此失败对照替代")
	})
	for _, direction := range []string{"scert_to_scert", "scert_to_official", "official_to_scert"} {
		t.Run(direction, func(t *testing.T) {
			target := server.Database(t)
			targetDSN, err := mysql.ParseDSN(target.DSN)
			if err != nil {
				t.Fatal(err)
			}
			grantMySQLViewAccess(t, server, parsed.User, targetDSN.DBName)
			targetDSN.User = parsed.User
			target.DSN = targetDSN.FormatDSN()
			verify, err := database.Open(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer verify.Close()
			if _, err := verify.DB.ExecContext(t.Context(), "CREATE VIEW a_nested AS SELECT 999 AS wrong_column"); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				input := output
				if direction == "official_to_scert" {
					input = official
				}
				if direction == "scert_to_official" {
					server.Import(t, target, input)
				} else if err := restoreMySQL(t.Context(), target, input); err != nil {
					t.Fatal(err)
				}
				if actual := readMySQLViewDefinitions(t, verify.DB); actual != original {
					t.Fatalf("恢复后的视图定义或列元数据不一致：\n源：%s\n目标：%s", original, actual)
				}
				checkMySQLInteropValue(t, target, "SELECT CONCAT(id, '|', amount, '|', label) FROM a_nested", "1|12345678901234567.12345678|原始值")
				checkMySQLInteropValue(t, target, "SELECT label FROM `legacy_café`", "café;;")
				if _, err := verify.DB.ExecContext(t.Context(), "UPDATE a_nested SET label = '更新值' WHERE id = 1"); err != nil {
					t.Fatal(err)
				}
				checkMySQLInteropValue(t, target, "SELECT `说明` FROM `中文``汇总`", "函数;更新值")
				if _, err := verify.DB.ExecContext(t.Context(), "UPDATE a_nested SET id = 3 WHERE id = 1"); err == nil {
					t.Fatal("恢复后视图的检查选项未阻止越界写入")
				}
				checkMySQLInteropValue(t, source, "SELECT label FROM source_values WHERE id = 1", "原始值")
			}
		})
	}
	after, err := os.ReadFile(official)
	if err != nil || sha256.Sum256(after) != originalDigest {
		t.Fatal("恢复过程改写了官方原始导出文件")
	}
	t.Run("existing_table_is_not_deleted", func(t *testing.T) {
		target := server.Database(t)
		dsn, err := mysql.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		grantMySQLViewAccess(t, server, parsed.User, dsn.DBName)
		dsn.User = parsed.User
		target.DSN = dsn.FormatDSN()
		verify, err := database.Open(t.Context(), target)
		if err != nil {
			t.Fatal(err)
		}
		defer verify.Close()
		for _, statement := range []string{"CREATE TABLE a_nested (marker INT)", "INSERT INTO a_nested VALUES (73)"} {
			if _, err := verify.DB.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := restoreMySQL(t.Context(), target, output); err == nil {
			t.Fatal("视图与目标已有基础表同名时没有拒绝恢复")
		}
		checkMySQLInteropValue(t, target, "SELECT marker FROM a_nested", "73")
	})
}

func grantMySQLViewAccess(t *testing.T, server *mysqltest.Server, user, name string) {
	t.Helper()
	grantMySQLProgramAccess(t, server, user, name)
	if _, err := server.Admin().ExecContext(t.Context(), "GRANT CREATE VIEW, SHOW VIEW ON "+quoteMySQLIdentifier(name)+".* TO "+mysqlStringLiteral(user)+"@'127.0.0.1'"); err != nil {
		t.Fatal(err)
	}
	if server.IsMySQL8() {
		if _, err := server.Admin().ExecContext(t.Context(), "GRANT SUPER ON *.* TO "+mysqlStringLiteral(user)+"@'127.0.0.1'"); err != nil {
			t.Fatal(err)
		}
	}
}

func readMySQLViewDefinitions(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT TABLE_NAME FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var values []string
	for _, name := range names {
		var actual, definition, charset, collation string
		if err := db.QueryRowContext(t.Context(), "SHOW CREATE VIEW "+quoteMySQLIdentifier(name)).Scan(&actual, &definition, &charset, &collation); err != nil {
			t.Fatal(err)
		}
		values = append(values, actual+"|"+definition+"|"+charset+"|"+collation)
	}
	columns, err := db.QueryContext(t.Context(), `SELECT JSON_ARRAY(c.TABLE_NAME, c.COLUMN_NAME, c.ORDINAL_POSITION,
	 c.COLUMN_TYPE, c.IS_NULLABLE, c.CHARACTER_SET_NAME, c.COLLATION_NAME)
	 FROM information_schema.COLUMNS c JOIN information_schema.VIEWS v
	 ON c.TABLE_SCHEMA = v.TABLE_SCHEMA AND c.TABLE_NAME = v.TABLE_NAME
	 WHERE c.TABLE_SCHEMA = DATABASE() ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`)
	if err != nil {
		t.Fatal(err)
	}
	defer columns.Close()
	for columns.Next() {
		var value string
		if err := columns.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := columns.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(values, "\n")
}

func TestMySQLRealViewPermissionFailure(t *testing.T) {
	server := mysqltest.New(t)
	cfg := server.Database(t)
	parsed, err := mysql.ParseDSN(cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Admin().ExecContext(t.Context(), "CREATE VIEW "+quoteMySQLIdentifier(parsed.DBName)+".hidden_definition AS SELECT 1 AS id"); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(server.Root(), "view-permissions.sql")
	if err := os.WriteFile(output, []byte("existing backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = backupMySQLDatabase(t.Context(), cfg, output)
	if err == nil || !strings.Contains(err.Error(), "SHOW VIEW") {
		t.Fatalf("缺少视图定义可见权限时必须明确拒绝：%v", err)
	}
	assertMySQLBackupFile(t, output, []byte("existing backup"))
	assertMySQLBackupAbsent(t, output+".tmp")
}
