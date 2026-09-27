package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealViewRestorePlanning(t *testing.T) {
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
	conn, err := db.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a := mysqlViewRestoreFixture("a", "select * from z")
	z := mysqlViewRestoreFixture("z", "select 1 AS id")
	input := append(slices.Clone(a), z...)
	original := slices.Clone(input)
	plan, err := prepareMySQLViewRestore(t.Context(), conn, input)
	if err != nil {
		t.Fatal(err)
	}
	want := append(slices.Clone(z), a...)
	if !slices.Equal(plan.statements, want) || !slices.Equal(input, original) {
		t.Fatal("恢复预检未整块排序，或改写了原始语句")
	}
	if err := plan.checkContext(t.Context(), conn, 0); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"INSERT INTO marker VALUES (99)", "SET @unrelated = 1", "USE another_database"} {
		t.Run(boundary, func(t *testing.T) {
			separated := append(append(slices.Clone(a), boundary), z...)
			plan, err := prepareMySQLViewRestore(t.Context(), conn, separated)
			if err != nil || !slices.Equal(plan.statements, separated) {
				t.Fatalf("视图块越过外部语句被重排：%v", err)
			}
		})
	}
	for _, statement := range []string{"SET SESSION sql_mode = 'ANSI_QUOTES'", "SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES'", "SET NAMES latin1"} {
		t.Run(statement, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if err := plan.checkContext(t.Context(), conn, 0); err == nil {
				t.Fatal("恢复时的实际语义变更没有被拒绝")
			}
			if _, err := conn.ExecContext(t.Context(), "SET SESSION sql_mode = ''"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(t.Context(), "SET NAMES utf8mb4"); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, statement := range []string{"CREATE TABLE marker(value INT) ENGINE=InnoDB", "INSERT INTO marker VALUES (73)"} {
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, scenario := range []string{"cycle", "duplicate", "name_mismatch", "invalid_charset"} {
		t.Run(scenario, func(t *testing.T) {
			blocks := slices.Clone(input)
			switch scenario {
			case "cycle":
				blocks = append(slices.Clone(a), mysqlViewRestoreFixture("z", "select * from a")...)
			case "duplicate":
				blocks = append(slices.Clone(a), a...)
			case "name_mismatch":
				blocks[0] = "/*!50001 DROP VIEW IF EXISTS `other` */"
			case "invalid_charset":
				blocks[4] = "/*!50001 SET character_set_client = unknown_charset */"
			}
			script := append([]string{"DROP TABLE marker"}, blocks...)
			file := filepath.Join(server.Root(), "view-plan-"+scenario+".sql")
			writeMySQLBackupFixture(t, file, []byte(strings.Join(script, ";\n")+";\n"))
			if err := restoreMySQL(t.Context(), cfg, file); err == nil || !strings.Contains(err.Error(), "预检 MySQL 视图恢复依赖失败") {
				t.Fatalf("无效视图恢复计划未在执行任何输入语句前拒绝：%v", err)
			}
			checkMySQLInteropValue(t, cfg, "SELECT value FROM marker", "73")
		})
	}
	other := server.Database(t)
	otherDSN, err := mysql.ParseDSN(other.DSN)
	if err != nil {
		t.Fatal(err)
	}
	grantMySQLViewAccess(t, server, dsn.User, otherDSN.DBName)
	if _, err := conn.ExecContext(t.Context(), "USE "+quoteMySQLIdentifier(otherDSN.DBName)); err != nil {
		t.Fatal(err)
	}
	if err := plan.checkContext(t.Context(), conn, 0); err == nil {
		t.Fatal("数据库切换后仍使用原库的依赖计划")
	}
}
