package cli

import (
	"reflect"
	"testing"
)

func TestSplitMySQLStatementsDelimiter(t *testing.T) {
	for _, item := range []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "procedure",
			script: "DELIMITER ;;\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT '值;;分号'; END;;\nDELIMITER ;\nSELECT 2;",
			want:   []string{"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT '值;;分号'; END", "SELECT 2"},
		},
		{
			name:   "quoted_crlf_bom",
			script: "\uFEFF-- 注释\r\n  dElImItEr '$$'\r\nSELECT '$$', `a$$b`, 'it''s';$$\r\nDELIMITER ;\r\nSELECT 2;",
			want:   []string{"SELECT '$$', `a$$b`, 'it''s';", "SELECT 2"},
		},
		{
			name:   "comments",
			script: "DELIMITER //\n/* // */ SELECT 1 # //\n//\nDELIMITER ;\nSELECT/**/2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "executable_comments",
			script: "DELIMITER ;;\n/*!50003 CREATE*/ /*!50003 TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET NEW.x = ';;'; END */;;\nDELIMITER ;",
			want:   []string{"/*!50003 CREATE*/ /*!50003 TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET NEW.x = ';;'; END */"},
		},
		{
			name:   "keyword_is_not_command",
			script: "SELECT delimiter FROM x;\nSELECT 'DELIMITER $$';\n-- DELIMITER $$\nSELECT 2;",
			want:   []string{"SELECT delimiter FROM x", "SELECT 'DELIMITER $$'", "SELECT 2"},
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, err := splitMySQLStatements(item.script)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, item.want) {
				t.Fatalf("分隔符解析不正确：得到 %#v，期望 %#v", got, item.want)
			}
		})
	}
}

func TestSplitMySQLStatementsRejectsMalformedScript(t *testing.T) {
	for _, script := range []string{
		"SELECT 1;\nDELIMITER\nSELECT 2;",
		"SELECT 1;\nDELIMITER a b\nSELECT 2;",
		"SELECT 1;\nDELIMITER " + string(rune(92)) + "\nSELECT 2;",
		"SELECT 1; SELECT '未闭合",
		"SELECT 1; SELECT `未闭合",
		"SELECT 1; /* 未闭合",
		"SELECT 1; /*!50003 未闭合",
	} {
		got, err := splitMySQLStatements(script)
		if err == nil || got != nil {
			t.Fatalf("格式错误时不应返回可执行的部分语句：%#v，%v", got, err)
		}
	}
}
