package database

import (
	"reflect"
	"testing"
)

func TestMigrationStatementsIgnoreOnlyLeadingBOM(t *testing.T) {
	want := []string{"ALTER TABLE servers ADD COLUMN auth_method VARCHAR(32)", "SELECT '\ufeff'"}
	for _, prefix := range []string{"", "\ufeff"} {
		got := migrationStatements([]byte(prefix + want[0] + ";\n" + want[1] + ";\n"))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("编码标记处理改变了 SQL：%q", got)
		}
	}
}

func TestEmbeddedMySQLPasswordMigrationSupportsResume(t *testing.T) {
	content, err := migrationFiles.ReadFile("migrations/mysql/0007_server_password_auth.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := migrationStatements(content)
	if len(statements) != 2 {
		t.Fatalf("密码认证迁移语句数量不正确：%d", len(statements))
	}
	for _, statement := range statements {
		if !mysqlAddColumnPattern.MatchString(statement) {
			t.Fatalf("迁移无法被字段重入检查识别：%q", statement)
		}
	}
}
