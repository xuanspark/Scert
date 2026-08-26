package repository

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func TestRepositoryRejectsNilDatabase(t *testing.T) {
	var repo *SQLDomainRepository
	ctx := context.Background()
	item, err := domain.NewDomain("example.com", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatalf("准备域名失败：%v", err)
	}
	if err := repo.Create(ctx, item); err == nil {
		t.Fatal("Create 未拒绝空数据库")
	}
	if _, err := repo.GetByID(ctx, item.ID); err == nil {
		t.Fatal("GetByID 未拒绝空数据库")
	}
	if _, err := repo.List(ctx); err == nil {
		t.Fatal("List 未拒绝空数据库")
	}
	if err := repo.Delete(ctx, item.ID); err == nil {
		t.Fatal("Delete 未拒绝空数据库")
	}
	if IsNotFound(nil) {
		t.Fatal("nil 错误不应被识别为未找到")
	}
	if !IsNotFound(sql.ErrNoRows) {
		t.Fatal("sql.ErrNoRows 未被识别为未找到")
	}
}

func TestRepositoryCRUD(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir).Database
	cfg.DSN = filepath.Join(dir, "data", "repository.db")
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移数据库失败：%v", err)
	}
	repo := NewSQLDomainRepository(db.DB)
	item, err := domain.NewDomain("example.com", domain.CertificateSAN, []string{"www.example.com"})
	if err != nil {
		t.Fatalf("创建域名模型失败：%v", err)
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("保存域名失败：%v", err)
	}
	loaded, err := repo.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("读取域名失败：%v", err)
	}
	if loaded.Name != item.Name || len(loaded.SANs) != 2 {
		t.Fatalf("读取域名内容不正确：%+v", loaded)
	}
	items, err := repo.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("域名列表不正确：%v，%+v", err, items)
	}
	if err := repo.Delete(context.Background(), item.ID); err != nil {
		t.Fatalf("删除域名失败：%v", err)
	}
	if err := repo.Delete(context.Background(), item.ID); !IsNotFound(err) {
		t.Fatalf("重复删除未返回未找到：%v", err)
	}
}
