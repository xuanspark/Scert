package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

func TestSQLAuditRepositoryCreatesLog(t *testing.T) {
	dir := t.TempDir()
	dbConfig := config.Default(dir).Database
	dbConfig.DSN = filepath.Join(dir, "data", "audit.db")
	db, err := database.Open(context.Background(), dbConfig)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移数据库失败：%v", err)
	}
	repo := NewSQLAuditRepository(db.DB)
	if err := repo.CreateAuditLog(context.Background(), AuditLog{Action: "admin_password_reset", ResourceType: "super_admin", ResourceID: "super-admin", Status: "success", Message: "密码重置成功"}); err != nil {
		t.Fatalf("写入审计日志失败：%v", err)
	}
	var count int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action = ? AND status = ?", "admin_password_reset", "success").Scan(&count); err != nil {
		t.Fatalf("查询审计日志失败：%v", err)
	}
	if count != 1 {
		t.Fatalf("审计日志数量不正确：%d", count)
	}
}

func TestSQLAuditRepositoryPrunesOnlyExpiredLogs(t *testing.T) {
	dir := t.TempDir()
	dbConfig := config.Default(dir).Database
	dbConfig.DSN = filepath.Join(dir, "data", "audit-prune.db")
	db, err := database.Open(context.Background(), dbConfig)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移数据库失败：%v", err)
	}

	repo := NewSQLAuditRepository(db.DB)
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	items := []AuditLog{
		{Action: "expired", ResourceType: "test", Status: "success", CreatedAt: cutoff.Add(-time.Second)},
		{Action: "boundary", ResourceType: "test", Status: "success", CreatedAt: cutoff},
		{Action: "retained", ResourceType: "test", Status: "success", CreatedAt: cutoff.Add(time.Second)},
	}
	for _, item := range items {
		if err := repo.CreateAuditLog(context.Background(), item); err != nil {
			t.Fatalf("写入审计日志失败：%v", err)
		}
	}

	deleted, err := repo.PruneExpiredAuditLogs(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("清理过期审计日志失败：%v", err)
	}
	if deleted != 1 {
		t.Fatalf("清理数量不正确：%d", deleted)
	}
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&count); err != nil {
		t.Fatalf("统计保留日志失败：%v", err)
	}
	if count != 2 {
		t.Fatalf("保留日志数量不正确：%d", count)
	}
	for _, action := range []string{"boundary", "retained"} {
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action).Scan(&count); err != nil {
			t.Fatalf("检查保留日志失败：%v", err)
		}
		if count != 1 {
			t.Fatalf("应保留审计日志 %s", action)
		}
	}
}
