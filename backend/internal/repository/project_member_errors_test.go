package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

type memberSQLiteCodeError int

func (e memberSQLiteCodeError) Error() string { return "测试数据库错误" }
func (e memberSQLiteCodeError) Code() int     { return int(e) }

func TestProjectMemberUniqueViolationClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"untyped_constraint", errors.New("constraint failed"), false},
		{"sqlite_primary", memberSQLiteCodeError(1555), true},
		{"sqlite_unique", memberSQLiteCodeError(2067), true},
		{"sqlite_trigger", memberSQLiteCodeError(1811), false},
		{"sqlite_foreign_key", memberSQLiteCodeError(787), false},
		{"sqlite_check", memberSQLiteCodeError(275), false},
		{"sqlite_not_null", memberSQLiteCodeError(1299), false},
		{"mysql_unique", &mysql.MySQLError{Number: 1062}, true},
		{"mysql_trigger", &mysql.MySQLError{Number: 1644}, false},
		{"mysql_foreign_key", &mysql.MySQLError{Number: 1452}, false},
		{"postgres_unique", &pgconn.PgError{Code: "23505"}, true},
		{"postgres_foreign_key", &pgconn.PgError{Code: "23503"}, false},
		{"postgres_trigger", &pgconn.PgError{Code: "P0001"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectMemberUniqueViolation(tc.err); got != tc.want {
				t.Fatalf("成员冲突错误分类不准确：%t，期望 %t", got, tc.want)
			}
			if tc.err != nil && projectMemberUniqueViolation(fmt.Errorf("包装错误：%w", tc.err)) != tc.want {
				t.Fatal("包装错误后的分类不一致")
			}
		})
	}
}
