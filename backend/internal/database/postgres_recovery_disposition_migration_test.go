package database

import (
	"fmt"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

func TestPostgresRealRecoveryDispositionMigration(t *testing.T) {
	server := postgrestest.New(t)
	for version := 22; version <= 25; version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			cfg := server.Database(t)
			db, err := Open(t.Context(), cfg)
			if err != nil {
				t.Fatal("隔离迁移连接失败")
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.DB.ExecContext(t.Context(), migrationTableSQL(cfg.Driver)); err != nil {
				t.Fatal(err)
			}
			for previous := 1; previous < version; previous++ {
				if err := applyMigration(t.Context(), db.DB, cfg.Driver, previous); err != nil {
					t.Fatal(err)
				}
			}
			content, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/postgres/%04d_%s.sql", version, migrationName(version)))
			if err != nil {
				t.Fatal(err)
			}
			// 已执行 DDL 但版本尚未落库时中止事务，不能留下半迁移结构。
			tx, err := db.DB.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range migrationStatements(content) {
				if _, err := tx.ExecContext(t.Context(), query); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			current, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil || current != version-1 {
				t.Fatal("回滚后版本记录改变", err)
			}
			for range 2 {
				if err := Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
					t.Fatal("中断后的迁移重入失败", err)
				}
			}
			current, err = currentMigrationVersion(t.Context(), db.DB)
			if err != nil || current != latestMigrationVersion {
				t.Fatal("迁移未达到当前版本", err)
			}
			for _, table := range []string{"recovery_dispositions", "recovery_claims", "recovery_events"} {
				var count int
				if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatal("处置表未正确初始化", err)
				}
			}
		})
	}
}
