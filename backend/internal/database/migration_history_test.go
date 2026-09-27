package database

import (
	"fmt"
	"strings"
	"testing"
)

func TestMigrateRejectsInvalidHistoryWithoutChangingDatabase(t *testing.T) {
	newerHistory := make([]int, latestMigrationVersion+1)
	for index := range newerHistory {
		newerHistory[index] = index + 1
	}
	cases := []struct {
		name     string
		versions []int
		message  string
	}{
		{"newer_database", newerHistory, "高于当前程序支持"},
		{"missing_first", []int{2}, "迁移历史不连续"},
		{"missing_middle", []int{1, 3}, "迁移历史不连续"},
		{"zero", []int{0}, "迁移历史不连续"},
		{"negative", []int{-1}, "迁移历史不连续"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.DB.ExecContext(t.Context(), migrationTableSQL("sqlite")); err != nil {
				t.Fatal(err)
			}
			for _, version := range tc.versions {
				if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE preserved_data (value TEXT); INSERT INTO preserved_data VALUES ('unchanged')"); err != nil {
				t.Fatal(err)
			}
			err = Migrate(t.Context(), db.DB, "sqlite")
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("未拒绝异常迁移历史：%v", err)
			}
			var count int
			if err := db.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != len(tc.versions) {
				t.Fatalf("拒绝迁移后版本记录被修改：%d", count)
			}
			if err := db.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 2 {
				t.Fatalf("拒绝迁移后创建了业务表：%d", count)
			}
			var value string
			if err := db.DB.QueryRow("SELECT value FROM preserved_data").Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != "unchanged" {
				t.Fatal("拒绝迁移后原数据被修改")
			}
		})
	}
}

func TestSQLiteUpgradeFromEverySupportedVersion(t *testing.T) {
	for version := 0; version <= latestMigrationVersion; version++ {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			db, err := Open(t.Context(), sqliteBusinessUpgradeConfig(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.DB.ExecContext(t.Context(), migrationTableSQL("sqlite")); err != nil {
				t.Fatal(err)
			}
			for next := 1; next <= version; next++ {
				if err := applyMigration(t.Context(), db.DB, "sqlite", next); err != nil {
					t.Fatal(err)
				}
			}
			for repeat := 0; repeat < 2; repeat++ {
				if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := currentMigrationVersion(t.Context(), db.DB)
			if err != nil || actual != latestMigrationVersion {
				t.Fatalf("迁移历史不完整：%d：%v", actual, err)
			}
		})
	}
}
