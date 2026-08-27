package repository

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
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
	item.Name = "www.example.com"
	item.CertificateType = domain.CertificateSingle
	item.SANs = []string{"www.example.com"}
	item.AutoRenew = false
	item.ACMEEnvironment = domain.ACMEEnvironmentProduction
	item.DNSProvider = "cloudflare"
	item.UpdatedAt = item.UpdatedAt.Add(time.Minute)
	if err := repo.Update(context.Background(), item); err != nil {
		t.Fatalf("更新域名失败：%v", err)
	}
	loaded, err = repo.GetByID(context.Background(), item.ID)
	if err != nil || loaded.Name != item.Name || loaded.AutoRenew || loaded.ACMEEnvironment != domain.ACMEEnvironmentProduction || loaded.DNSProvider != "cloudflare" {
		t.Fatalf("更新后的域名内容不正确：%v，%+v", err, loaded)
	}
	items, err := repo.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("域名列表不正确：%v，%+v", err, items)
	}
	serverRepo := NewSQLServerRepository(db.DB)
	server := deployment.Server{ID: "server-1", Name: "服务器", Address: "192.0.2.30", Username: "deploy", PrivateKeyRef: "secret://ssh/1", HostKeyFingerprint: "SHA256:test", CertificatePath: "/etc/scert/cert.pem", PrivateKeyPath: "/etc/scert/key.pem", CertificateChainPath: "/etc/scert/fullchain.pem", WebServer: deployment.WebServerNginx}
	if err := serverRepo.CreateServer(context.Background(), server); err != nil {
		t.Fatalf("创建关联服务器失败：%v", err)
	}
	if err := serverRepo.ReplaceDomainServers(context.Background(), item.ID, []string{server.ID}); err != nil {
		t.Fatalf("创建域名服务器关联失败：%v", err)
	}
	if err := repo.Delete(context.Background(), item.ID); err != nil {
		t.Fatalf("删除域名失败：%v", err)
	}
	associated, err := serverRepo.ListServersByDomainID(context.Background(), item.ID)
	if err != nil || len(associated) != 0 {
		t.Fatalf("删除域名后不应保留服务器关联：%v %+v", err, associated)
	}
	if err := repo.Delete(context.Background(), item.ID); !IsNotFound(err) {
		t.Fatalf("重复删除未返回未找到：%v", err)
	}
}
