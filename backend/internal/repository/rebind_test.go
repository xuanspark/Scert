package repository

import "testing"

func TestRebindPostgresParameters(t *testing.T) {
	query := "SELECT * FROM tasks WHERE domain_id = ? AND status = ?"
	want := "SELECT * FROM tasks WHERE domain_id = $1 AND status = $2"
	if got := Rebind("postgres", query); got != want {
		t.Fatalf("PostgreSQL 占位符转换错误：%q", got)
	}
}

func TestRebindDoesNotChangeQuotedQuestionMarks(t *testing.T) {
	query := "SELECT '?' AS literal, \"?\" AS identifier, `?` AS legacy_identifier WHERE id = ? AND note = 'it''s ?'"
	want := "SELECT '?' AS literal, \"?\" AS identifier, `?` AS legacy_identifier WHERE id = $1 AND note = 'it''s ?'"
	if got := Rebind("postgres", query); got != want {
		t.Fatalf("字符串或标识符中的问号被错误转换：%q", got)
	}
}

func TestRebindLeavesSQLiteAndMySQLUntouched(t *testing.T) {
	query := "SELECT * FROM tasks WHERE id = ?"
	for _, driver := range []string{"sqlite", "mysql", ""} {
		if got := Rebind(driver, query); got != query {
			t.Fatalf("%s 的占位符不应改变：%q", driver, got)
		}
	}
}
