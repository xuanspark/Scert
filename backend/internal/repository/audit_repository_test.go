package repository

import (
	"context"
	"path/filepath"
	"testing"

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
