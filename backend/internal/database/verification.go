package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

const verificationTablePrefix = "scert_bootstrap_verify_"

// VerifyWritable 通过随机临时表执行创建、写入、读取和清理，验证数据库账号具备运行所需写权限。
// cfg 必须是 ResolveConfig 返回的仅内存配置，因其中可能含有数据库密码。
func VerifyWritable(ctx context.Context, cfg config.DatabaseConfig) (err error) {
	db, err := Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	tableName, err := newVerificationTableName()
	if err != nil {
		return err
	}
	value, err := newVerificationValue()
	if err != nil {
		return err
	}
	quotedTable := quoteIdentifier(cfg.Driver, tableName)
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanupErr := dropVerificationTable(cleanupContext, db.DB, quotedTable)
		if cleanupErr != nil && err == nil {
			err = fmt.Errorf("清理数据库验证临时表失败：%w", cleanupErr)
		}
	}()

	if _, err := db.DB.ExecContext(ctx, "CREATE TABLE "+quotedTable+" (value TEXT NOT NULL)"); err != nil {
		return fmt.Errorf("创建数据库验证临时表失败：%w", err)
	}
	placeholder := "?"
	if strings.EqualFold(cfg.Driver, "postgres") {
		placeholder = "$1"
	}
	if _, err := db.DB.ExecContext(ctx, "INSERT INTO "+quotedTable+" (value) VALUES ("+placeholder+")", value); err != nil {
		return fmt.Errorf("写入数据库验证临时表失败：%w", err)
	}
	var actual string
	if err := db.DB.QueryRowContext(ctx, "SELECT value FROM "+quotedTable+" LIMIT 1").Scan(&actual); err != nil {
		return fmt.Errorf("读取数据库验证临时表失败：%w", err)
	}
	if actual != value {
		return errors.New("数据库验证读取结果不一致")
	}
	return nil
}

func newVerificationTableName() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成数据库验证临时表名失败：%w", err)
	}
	return verificationTablePrefix + hex.EncodeToString(buffer), nil
}

func newVerificationValue() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成数据库验证随机值失败：%w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func quoteIdentifier(driver, identifier string) string {
	if strings.EqualFold(driver, "mysql") {
		return "`" + identifier + "`"
	}
	return `"` + identifier + `"`
}

func dropVerificationTable(ctx context.Context, db *sql.DB, table string) error {
	_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table)
	return err
}
