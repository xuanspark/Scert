package repository

import (
	"errors"
	"fmt"
	"testing"
)

type sqliteCodeFixture int

func (e sqliteCodeFixture) Error() string { return "测试驱动错误，不依赖文本识别" }
func (e sqliteCodeFixture) Code() int     { return int(e) }

func TestSQLiteWriteConflictClassification(t *testing.T) {
	for _, code := range []int{5, 6, 261, 517, 773, 262} {
		if !isSQLiteWriteConflict(fmt.Errorf("外层错误：%w", sqliteCodeFixture(code))) {
			t.Fatal("忙或锁扩展错误码未被识别", code)
		}
	}
	for _, err := range []error{nil, errors.New("database is locked"), sqliteCodeFixture(19), sqliteCodeFixture(2067), sqliteCodeFixture(10)} {
		if isSQLiteWriteConflict(err) {
			t.Fatal("非锁冲突不能伪装成可重试版本冲突", err)
		}
	}
}
