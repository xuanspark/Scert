package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestSQLiteUpgradePreservesUngroupedUsers(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.ExecContext(t.Context(), migrationTableSQL("sqlite")); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 7; version++ {
		if err := applyMigration(t.Context(), db.DB, "sqlite", version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO users
		(id, username, email, password_hash, role, status)
		VALUES ('legacy', 'legacy', '', 'existing-hash', 'admin', 'active')`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := Migrate(t.Context(), db.DB, "sqlite"); err != nil {
			t.Fatal(err)
		}
	}
	var username, passwordHash, role, status string
	var groups sql.NullString
	if err := db.DB.QueryRowContext(t.Context(), `SELECT username, password_hash, role, status, groups_json
		FROM users WHERE id = 'legacy'`).Scan(&username, &passwordHash, &role, &status, &groups); err != nil {
		t.Fatal(err)
	}
	if username != "legacy" || passwordHash != "existing-hash" || role != "admin" || status != "active" {
		t.Fatalf("unexpected legacy user: %q %q %q", username, role, status)
	}
	if groups.Valid && groups.String != "[]" {
		t.Fatalf("unexpected legacy groups: %v", groups)
	}
	var version int
	if err := db.DB.QueryRowContext(t.Context(), "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != latestMigrationVersion {
		t.Fatalf("unexpected version: %d", version)
	}
}
