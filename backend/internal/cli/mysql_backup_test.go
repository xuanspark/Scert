package cli

import (
	"database/sql"
	"strings"
	"testing"
)

func TestQuoteMySQLIdentifierEscapesBackticks(t *testing.T) {
	if value := quoteMySQLIdentifier("a`b"); value != "`a``b`" {
		t.Fatalf("MySQL 标识符转义不正确：%s", value)
	}
}

func TestMySQLSQLLiteral(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "null", value: nil, want: "NULL"},
		{name: "string", value: "a'b\\c\n\x00\x1a", want: "'a''b\\\\c\\n\\0\\Z'"},
		{name: "binary", value: []byte{0x01, 0xaf}, want: "X'01af'"},
		{name: "raw bytes", value: sql.RawBytes{0x00, 0xff}, want: "X'00ff'"},
		{name: "integer", value: int64(42), want: "42"},
		{name: "boolean", value: true, want: "1"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if value := mysqlSQLLiteral(item.value); value != item.want {
				t.Fatalf("SQL 字面量不正确：%s", value)
			}
		})
	}
}

func TestQuoteMySQLColumns(t *testing.T) {
	value := quoteMySQLColumns([]string{"id", "display_name"})
	if value != "`id`, `display_name`" || strings.Contains(value, "'") {
		t.Fatalf("MySQL 字段列表不正确：%s", value)
	}
}

func TestMySQLColumnSQLLiteral(t *testing.T) {
	for _, item := range []struct {
		name       string
		value      any
		columnType string
		want       string
	}{
		{name: "decimal", value: []byte("123456789.123456"), columnType: "DECIMAL", want: "'123456789.123456'"},
		{name: "precision", value: sql.RawBytes("-12345678901234567890123456789012345.123456789012345678901234567890"), columnType: "DECIMAL", want: "'-12345678901234567890123456789012345.123456789012345678901234567890'"},
		{name: "null", value: nil, columnType: "DECIMAL", want: "NULL"},
		{name: "string", value: "0.000001", columnType: "DECIMAL", want: "'0.000001'"},
		{name: "binary", value: []byte("12"), columnType: "BLOB", want: "X'3132'"},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := mysqlColumnSQLLiteral(item.value, item.columnType); got != item.want {
				t.Fatalf("字段字面量不正确：%s", got)
			}
		})
	}
}

func TestSplitMySQLStatementsKeepsQuotedSemicolons(t *testing.T) {
	script := "-- 注释\nSET FOREIGN_KEY_CHECKS=0;\nINSERT INTO `items` (`value`) VALUES ('a; b'), ('it''s');\n"
	statements, err := splitMySQLStatements(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 {
		t.Fatalf("SQL 语句拆分数量不正确：%d %#v", len(statements), statements)
	}
	if strings.Contains(statements[0], "注释") || !strings.Contains(statements[1], "'a; b'") || !strings.Contains(statements[1], "'it''s'") {
		t.Fatalf("SQL 语句拆分内容不正确：%#v", statements)
	}
}

func TestSplitMySQLStatementsKeepsExecutableComments(t *testing.T) {
	script := "CREATE TABLE `items` (`id` bigint) /*!50100 SET @value = 'a;b' */;\n"
	statements, err := splitMySQLStatements(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 {
		t.Fatalf("带可执行注释的 SQL 语句拆分数量不正确：%d %#v", len(statements), statements)
	}
	if !strings.Contains(statements[0], "/*!50100 SET @value = 'a;b' */") {
		t.Fatalf("MySQL 可执行注释不应被删除：%s", statements[0])
	}
}
