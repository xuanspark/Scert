package cli

import (
	"fmt"
	"slices"
	"testing"
)

func mysqlViewRestoreFixture(name, query string) []string {
	plain := []string{
		"DROP VIEW IF EXISTS " + quoteMySQLIdentifier(name),
		"SET @saved_cs_client = @@character_set_client",
		"SET @saved_cs_results = @@character_set_results",
		"SET @saved_col_connection = @@collation_connection",
		"SET character_set_client = utf8mb4",
		"SET character_set_results = utf8mb4",
		"SET collation_connection = utf8mb4_bin",
		"CREATE ALGORITHM=MERGE SQL SECURITY INVOKER VIEW " + quoteMySQLIdentifier(name) + " AS " + query,
		"SET character_set_client = @saved_cs_client",
		"SET character_set_results = @saved_cs_results",
		"SET collation_connection = @saved_col_connection",
	}
	for i := range plain {
		plain[i] = "/*!50001 " + plain[i] + " */"
	}
	return plain
}

func TestMySQLViewRestoreBlock(t *testing.T) {
	statements := mysqlViewRestoreFixture("a", "select * from z")
	block, ok := readMySQLViewRestoreBlock(statements)
	if !ok || block.name != "a" || block.charset != "utf8mb4" || !slices.Equal(block.statements, statements) {
		t.Fatal("完整官方视图块未被原样识别")
	}
	for _, test := range []struct {
		name string
		at   int
		sql  string
	}{
		{"missing_restore", 10, "SELECT 1"},
		{"business_statement", 5, "/*!50001 DELETE FROM user_data */"},
		{"global_setting", 4, "/*!50001 SET GLOBAL character_set_client = utf8mb4 */"},
		{"setting_expression", 4, "/*!50001 SET character_set_client = CONCAT('utf8','mb4') */"},
		{"unknown_restore", 8, "/*!50001 SET character_set_client = @another */"},
		{"extra_assignment", 8, "/*!50001 SET character_set_client = @saved_cs_client, sql_mode = '' */"},
		{"split_identifier", 8, "/*!50001 SET character_set_ client = @saved_cs_client */"},
		{"plain_create", 7, "CREATE VIEW `a` AS SELECT 1"},
		{"future_version", 7, "/*!99999 CREATE ALGORITHM=MERGE VIEW `a` AS SELECT 1 */"},
		{"qualified_drop", 0, "/*!50001 DROP VIEW IF EXISTS `db`.`a` */"},
		{"multiple_drops", 0, "/*!50001 DROP VIEW IF EXISTS `a`, `b` */"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := slices.Clone(statements)
			changed[test.at] = test.sql
			if _, ok := readMySQLViewRestoreBlock(changed); ok {
				t.Fatal("非闭合官方视图块不能被重排")
			}
		})
	}
	for length := 0; length < 11; length++ {
		t.Run(fmt.Sprintf("truncated_%d", length), func(t *testing.T) {
			if _, ok := readMySQLViewRestoreBlock(statements[:length]); ok {
				t.Fatal("截断视图块不能被重排")
			}
		})
	}
}

func TestMySQLViewDumpWrapper(t *testing.T) {
	for _, source := range []string{
		"/*!50001 CREATE ALGORITHM=MERGE */\n/*!50013 DEFINER=`a`@`b` SQL SECURITY INVOKER */\n/*!50001 VIEW `a` AS SELECT 1 */",
		"/*!50001 CREATE ALGORITHM=MERGE VIEW `a` AS SELECT 1 */ /*!50002 WITH LOCAL CHECK OPTION */",
	} {
		plain, ok := unwrapMySQLViewDumpStatement(source)
		if !ok {
			t.Fatal("官方版本化定义无法展开")
		}
		if name, err := mysqlViewCreateName(plain); err != nil || name != "a" {
			t.Fatalf("视图名称识别失败：%v", err)
		}
	}
	for _, source := range []string{"", "CREATE VIEW `a` AS SELECT 1", "/*!50001 unfinished", "/*!99999 VIEW a */", "/*!50001SELECT 1 */", "/*!50001 VIEW a */; DELETE FROM t", "/* plain */"} {
		if _, ok := unwrapMySQLViewDumpStatement(source); ok {
			t.Fatal("未知封装被识别成官方视图定义")
		}
	}
}
