package repository

import (
	"context"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func TestServerRepositoryCRUDAndDomainAssociations(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir).Database
	cfg.DSN = filepath.Join(dir, "data", "servers.db")
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLServerRepository(db.DB)
	server := deployment.Server{
		ID: "server-1", Name: "生产服务器", Address: "192.0.2.10", Port: 22, Username: "deploy",
		PrivateKeyRef: "secret://ssh/server-1", HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/etc/scert/cert.pem", PrivateKeyPath: "/etc/scert/key.pem",
		CertificateChainPath: "/etc/scert/fullchain.pem", WebServer: deployment.WebServerNginx,
	}
	if err := repo.CreateServer(context.Background(), server); err != nil {
		t.Fatalf("创建服务器失败：%v", err)
	}
	loaded, err := repo.GetServerByID(context.Background(), server.ID)
	if err != nil {
		t.Fatalf("读取服务器失败：%v", err)
	}
	if loaded.Name != server.Name || loaded.ValidationCommand[0] != "nginx" || loaded.ValidationCommand[1] != "-t" || loaded.ReloadCommand[0] != "nginx" || loaded.CreatedAt.IsZero() {
		t.Fatalf("服务器内容错误：%+v", loaded)
	}
	server.Name = "生产服务器-更新"
	server.WebServer = deployment.WebServerCaddy
	server.ReloadCommand = nil
	if err := repo.UpdateServer(context.Background(), server); err != nil {
		t.Fatalf("更新服务器失败：%v", err)
	}
	updated, err := repo.GetServerByID(context.Background(), server.ID)
	if err != nil {
		t.Fatalf("load updated server: %v", err)
	}
	if updated.CreatedAt.IsZero() || updated.UpdatedAt.IsZero() || updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("updated server timestamps are invalid: %+v", updated)
	}
	item, err := domain.NewDomain("example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = "domain-1"
	if err := NewSQLDomainRepository(db.DB).Create(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceDomainServers(context.Background(), "domain-1", []string{server.ID, server.ID}); err != nil {
		t.Fatalf("保存域名服务器关联失败：%v", err)
	}
	associated, err := repo.ListServersByDomainID(context.Background(), "domain-1")
	if err != nil || len(associated) != 1 || associated[0].WebServer != deployment.WebServerCaddy {
		t.Fatalf("域名服务器关联错误：%v %+v", err, associated)
	}
	servers, err := repo.ListServers(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("服务器列表错误：%v %+v", err, servers)
	}
	if err := repo.DeleteServer(context.Background(), server.ID); err != nil {
		t.Fatalf("删除服务器失败：%v", err)
	}
	associated, err = repo.ListServersByDomainID(context.Background(), "domain-1")
	if err != nil {
		t.Fatalf("读取删除后的域名服务器关联失败：%v", err)
	}
	if len(associated) != 0 {
		t.Fatalf("删除服务器后不应保留悬空关联：%+v", associated)
	}
}

func TestServerRepositoryRejectsInvalidServer(t *testing.T) {
	db, err := database.Open(context.Background(), config.DatabaseConfig{
		Driver: "sqlite", DSN: ":memory:", MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := NewSQLServerRepository(db.DB).CreateServer(context.Background(), deployment.Server{}); err == nil {
		t.Fatal("无效服务器配置应被拒绝")
	}
}

func TestServerRepositoryReportsPrivateKeyReferenceUsage(t *testing.T) {
	db := openServerTestDatabase(t)
	defer db.Close()
	repo := NewSQLServerRepository(db.DB)
	server := validTestServer()
	server.ID = "server-private-key-usage"
	server.PrivateKeyRef = "secret://ssh/used-key"
	if err := repo.CreateServer(t.Context(), server); err != nil {
		t.Fatalf("创建测试服务器失败：%v", err)
	}
	used, err := repo.IsPrivateKeyRefUsed(t.Context(), server.PrivateKeyRef)
	if err != nil || !used {
		t.Fatalf("未识别已占用的 SSH 私钥引用：used=%t err=%v", used, err)
	}
	used, err = repo.IsPrivateKeyRefUsed(t.Context(), "secret://ssh/not-used")
	if err != nil || used {
		t.Fatalf("错误识别未占用的 SSH 私钥引用：used=%t err=%v", used, err)
	}
}

func TestReplaceDomainServersRejectsUnknownServerWithoutChangingAssociations(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir).Database
	cfg.DSN = filepath.Join(dir, "data", "association.db")
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLServerRepository(db.DB)
	server := deployment.Server{ID: "server-1", Name: "服务器", Address: "192.0.2.20", Username: "deploy", PrivateKeyRef: "secret://ssh/1", HostKeyFingerprint: "SHA256:test", CertificatePath: "/etc/scert/cert.pem", PrivateKeyPath: "/etc/scert/key.pem", CertificateChainPath: "/etc/scert/fullchain.pem", WebServer: deployment.WebServerNginx}
	if err := repo.CreateServer(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	item, err := domain.NewDomain("association.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = "domain-1"
	if err := NewSQLDomainRepository(db.DB).Create(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceDomainServers(context.Background(), "domain-1", []string{server.ID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceDomainServers(context.Background(), "domain-1", []string{"missing"}); err == nil {
		t.Fatal("未知服务器应被拒绝")
	}
	items, err := repo.ListServersByDomainID(context.Background(), "domain-1")
	if err != nil || len(items) != 1 || items[0].ID != server.ID {
		t.Fatalf("失败后原关联应保持不变：%v %+v", err, items)
	}
}

func openServerTestDatabase(t *testing.T) *database.Database {
	t.Helper()
	directory := t.TempDir()
	cfg := config.Default(directory).Database
	cfg.DSN = filepath.Join(directory, "data", "server-test.db")
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("打开服务器测试数据库失败：%v", err)
	}
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		_ = db.Close()
		t.Fatalf("迁移服务器测试数据库失败：%v", err)
	}
	return db
}

func validTestServer() deployment.Server {
	return deployment.Server{
		Name: "测试服务器", Address: "192.0.2.70", Port: 22, Username: "deploy",
		PrivateKeyRef: "secret://ssh/test", HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/etc/scert/cert.pem", PrivateKeyPath: "/etc/scert/key.pem",
		CertificateChainPath: "/etc/scert/fullchain.pem", WebServer: deployment.WebServerNginx,
	}
}
