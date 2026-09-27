package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealViewDependencies(t *testing.T) {
	server := mysqltest.New(t)
	source := server.Database(t)
	dsn, err := mysql.ParseDSN(source.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLViewAccess(t, server, dsn.User, dsn.DBName)
	external := server.Database(t)
	other, err := mysql.ParseDSN(external.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLViewAccess(t, server, dsn.User, other.DBName)
	if _, err := server.Admin().ExecContext(t.Context(), "CREATE VIEW "+quoteMySQLIdentifier(other.DBName)+".z_external AS SELECT 41 AS id"); err != nil {
		t.Fatal(err)
	}
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
	statements := []string{
		"CREATE TABLE source_values (id INT PRIMARY KEY, label VARCHAR(30), stamp DATE) ENGINE=InnoDB",
		"INSERT INTO source_values VALUES (1, ' xx ', '2026-01-02')",
		"CREATE VIEW z_data AS SELECT * FROM source_values",
		"CREATE VIEW a_expression AS SELECT id, TRIM(BOTH ' ' FROM label) AS label, EXTRACT(YEAR FROM stamp) AS year_value FROM z_data",
		"CREATE VIEW a_scalar AS SELECT (SELECT MAX(id) FROM z_data) AS id",
		"CREATE VIEW a_join AS SELECT x.id FROM (z_data x JOIN z_data y ON x.id=y.id), z_data z WHERE z.id=x.id",
		"CREATE VIEW a_derived AS SELECT id FROM (SELECT id FROM z_data UNION ALL SELECT id FROM z_data) x",
		"CREATE VIEW a_external AS SELECT id FROM " + quoteMySQLIdentifier(other.DBName) + ".z_external",
		"CREATE VIEW z_external AS SELECT id FROM a_external",
		"SET SESSION sql_mode = 'ANSI_QUOTES,NO_BACKSLASH_ESCAPES'",
		"CREATE VIEW a_mode AS SELECT 'from z_data' AS `from`, 'join z_external' AS `join`, id FROM z_data",
		"SET SESSION sql_mode = ''",
	}
	if server.IsMySQL8() {
		statements = append(statements,
			"CREATE VIEW a_cte AS WITH z_data AS (SELECT id FROM z_data) SELECT id FROM z_data",
			"CREATE VIEW a_recursive AS WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<3) SELECT n FROM seq",
			"CREATE VIEW a_cte_scope AS SELECT id FROM (WITH z_data AS (SELECT id FROM z_data) SELECT id FROM z_data) x")
	}
	// 超过 InnoDB 基础表列数上限，证明导出不再依赖占位表。
	var columns []string
	for i := 0; i < 1020; i++ {
		columns = append(columns, fmt.Sprintf("%d AS c%d", i, i))
	}
	statements = append(statements, "CREATE VIEW wide_view AS SELECT "+strings.Join(columns, ","))
	for _, statement := range statements {
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	original := readMySQLViewDefinitions(t, db.DB)
	output := filepath.Join(server.Root(), "complex-views.sql")
	if err := backupMySQLDatabase(t.Context(), source, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "CREATE TABLE `wide_view`") || strings.Contains(string(data), "DROP TABLE `wide_view`") {
		t.Fatal("视图导出仍在创建或删除占位基础表")
	}
	for _, direction := range []string{"scert", "official"} {
		t.Run(direction, func(t *testing.T) {
			target := server.Database(t)
			parsed, err := mysql.ParseDSN(target.DSN)
			if err != nil {
				t.Fatal(err)
			}
			grantMySQLViewAccess(t, server, dsn.User, parsed.DBName)
			parsed.User = dsn.User
			target.DSN = parsed.FormatDSN()
			for attempt := 0; attempt < 2; attempt++ {
				if direction == "official" {
					server.Import(t, target, output)
				} else if err := restoreMySQL(t.Context(), target, output); err != nil {
					t.Fatal(err)
				}
			}
			verify, err := database.Open(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer verify.Close()
			if actual := readMySQLViewDefinitions(t, verify.DB); actual != original {
				t.Fatal("复杂视图恢复后的原始定义或列元数据发生变化")
			}
			for query, expected := range map[string]string{
				"SELECT CONCAT(id, '|', label, '|', year_value) FROM a_expression": "1|xx|2026",
				"SELECT id FROM a_scalar":        "1",
				"SELECT id FROM a_join":          "1",
				"SELECT COUNT(*) FROM a_derived": "2",
				"SELECT id FROM z_external":      "41",
				"SELECT c1019 FROM wide_view":    "1019",
			} {
				checkMySQLInteropValue(t, target, query, expected)
			}
			if server.IsMySQL8() {
				checkMySQLInteropValue(t, target, "SELECT id FROM a_cte", "1")
				checkMySQLInteropValue(t, target, "SELECT id FROM a_cte_scope", "1")
				checkMySQLInteropValue(t, target, "SELECT SUM(n) FROM a_recursive", "6")
			}
		})
	}
}

func TestMySQLRealInvalidViewPreservesBackup(t *testing.T) {
	server := mysqltest.New(t)
	cfg := server.Database(t)
	dsn, err := mysql.ParseDSN(cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLViewAccess(t, server, dsn.User, dsn.DBName)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE TABLE source_values(id INT) ENGINE=InnoDB",
		"CREATE VIEW broken_view AS SELECT id FROM source_values",
		"DROP TABLE source_values",
	} {
		if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(server.Root(), "invalid-view.sql")
	original := []byte("保留原备份")
	writeMySQLBackupFixture(t, output, original)
	if err := backupMySQLDatabase(t.Context(), cfg, output); err == nil {
		t.Fatal("失效视图不能导出为成功备份")
	}
	assertMySQLBackupFile(t, output, original)
	assertMySQLBackupAbsent(t, output+".tmp")
}
