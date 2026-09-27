package cli

import (
	"strings"
	"testing"
)

func TestMySQLRoutineHeaderEnd(t *testing.T) {
	for _, header := range []string{
		"CREATE DEFINER=`user`@`host` FUNCTION `name`",
		"CREATE DEFINER=`u(s``er`@`host` PROCEDURE `na(me`",
		`CREATE DEFINER="u(ser"@"host" FUNCTION "na""me"`,
	} {
		end, err := mysqlRoutineHeaderEnd(header + "(IN value INT) BEGIN SELECT 1; END")
		if err != nil || end != len(header) {
			t.Fatalf("声明头边界不正确：%d，%v", end, err)
		}
	}
	if _, err := mysqlRoutineHeaderEnd("CREATE FUNCTION `unterminated()"); err == nil || !strings.Contains(err.Error(), "参数列表") {
		t.Fatal("不完整声明头必须失败")
	}
}

func TestMySQLRoutineVisibilityGrants(t *testing.T) {
	for _, item := range []struct {
		name     string
		grants   []string
		foldCase bool
		want     bool
	}{
		{"show_routine", []string{"GRANT SHOW_ROUTINE ON *.* TO 'u'@'h'"}, false, true},
		{"global_select", []string{"GRANT SELECT ON *.* TO 'u'@'h'", "GRANT BACKUP_ADMIN ON *.* TO 'u'@'h'"}, false, true},
		{"static_all", []string{"GRANT ALL PRIVILEGES ON *.* TO 'u'@'h'"}, false, true},
		{"schema_select", []string{"GRANT SELECT ON `sample`.* TO 'u'@'h'"}, false, false},
		{"schema_all", []string{"GRANT ALL PRIVILEGES ON `sample`.* TO 'u'@'h'"}, false, false},
		{"role_name", []string{"GRANT `SHOW_ROUTINE`@`%` TO 'u'@'h'"}, false, false},
		{"execute_only", []string{"GRANT EXECUTE, CREATE ROUTINE ON *.* TO 'u'@'h'"}, false, false},
		{"partial_revoke", []string{"GRANT SELECT ON *.* TO 'u'@'h'", "REVOKE SELECT ON `sample`.* FROM 'u'@'h'"}, false, false},
		{"revoke_other_schema", []string{"GRANT SELECT ON *.* TO 'u'@'h'", "REVOKE SELECT ON `other`.* FROM 'u'@'h'"}, false, true},
		{"show_not_revoked", []string{"GRANT SELECT ON *.* TO 'u'@'h'", "REVOKE SELECT ON `sample`.* FROM 'u'@'h'", "GRANT SHOW_ROUTINE ON *.* TO 'u'@'h'"}, false, true},
		{"fold_revoke", []string{"GRANT SELECT ON *.* TO 'u'@'h'", "REVOKE SELECT ON `SAMPLE`.* FROM 'u'@'h'"}, true, false},
		{"case_sensitive", []string{"GRANT SELECT ON *.* TO 'u'@'h'", "REVOKE SELECT ON `SAMPLE`.* FROM 'u'@'h'"}, false, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := mysqlRoutineVisibilityGranted(item.grants, "sample", item.foldCase); got != item.want {
				t.Fatalf("例程可见权限判定错误：得到 %t，期望 %t", got, item.want)
			}
		})
	}
}
