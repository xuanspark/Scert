package repository

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestResourceProjectSQLite(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "resource-project.db")
	checkResourceProjects(t, cfg)
}

func TestResourceProjectMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkResourceProjects(t, server.Database(t))
}

func checkResourceProjects(t *testing.T, cfg config.DatabaseConfig) {
	t.Helper()
	ctx := t.Context()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(ctx, db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	domains := NewSQLDomainRepository(db.DB, cfg.Driver)
	servers := NewSQLServerRepository(db.DB, cfg.Driver)
	for _, id := range []string{"project-a", "project-b"} {
		if err := projects.CreateProject(ctx, domain.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	item, err := domain.NewDomain("resource.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.ResourceScope{ProjectID: "project-a", OwnerID: "owner", CreatedBy: "creator", UpdatedBy: "creator"}
	item.ResourceScope = scope
	if err := domains.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	server := deployment.Server{
		ResourceScope: scope,
		ID:            "server-a", Name: "项目服务器", Address: "192.0.2.10", Port: 22, Username: "deploy",
		AuthMethod: deployment.SSHAuthPassword, PasswordRef: "secret://ssh/password/test", HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/test/cert.pem", PrivateKeyPath: "/test/key.pem",
		CertificateChainPath: "/test/chain.pem", WebServer: deployment.WebServerNginx,
	}
	if err := servers.CreateServer(ctx, server); err != nil {
		t.Fatal(err)
	}
	other := server
	other.ID = "server-b"
	other.ProjectID = "project-b"
	if err := servers.CreateServer(ctx, other); err != nil {
		t.Fatal(err)
	}
	t.Run("persisted_scope", func(t *testing.T) {
		loaded, err := domains.GetByID(ctx, item.ID)
		if err != nil || loaded.ResourceScope != scope {
			t.Fatalf("域名归属未持久化：%+v，%v", loaded.ResourceScope, err)
		}
		loadedServer, err := servers.GetServerByID(ctx, server.ID)
		if err != nil || loadedServer.ResourceScope != scope {
			t.Fatalf("服务器归属未持久化：%+v，%v", loadedServer.ResourceScope, err)
		}
		items, err := domains.List(ctx)
		if err != nil || len(items) != 1 || items[0].ResourceScope != scope {
			t.Fatalf("域名列表丢失归属：%v", err)
		}
		serverItems, err := servers.ListServers(ctx)
		if err != nil || len(serverItems) != 2 {
			t.Fatalf("服务器列表错误：%v", err)
		}
		for _, value := range serverItems {
			if value.OwnerID != scope.OwnerID || value.CreatedBy != scope.CreatedBy || value.ProjectID == "" {
				t.Fatal("服务器列表丢失归属")
			}
		}
	})
	t.Run("update_preserves_identity", func(t *testing.T) {
		changed := item
		changed.OwnerID, changed.CreatedBy, changed.UpdatedBy = "forged-owner", "forged-creator", "editor"
		changed.Name = "updated.example.test"
		if err := domains.Update(ctx, changed); err != nil {
			t.Fatal(err)
		}
		changedServer := server
		changedServer.OwnerID, changedServer.CreatedBy, changedServer.UpdatedBy = "forged-owner", "forged-creator", "editor"
		if err := servers.UpdateServer(ctx, changedServer); err != nil {
			t.Fatal(err)
		}
		// 包含 MySQL 实际没有字段变化时的重复更新。
		if err := domains.Update(ctx, changed); err != nil {
			t.Fatal(err)
		}
		loaded, err := domains.GetByID(ctx, item.ID)
		if err != nil || loaded.OwnerID != "owner" || loaded.CreatedBy != "creator" || loaded.UpdatedBy != "editor" {
			t.Fatalf("域名更新错误覆盖归属：%+v，%v", loaded.ResourceScope, err)
		}
		loadedServer, err := servers.GetServerByID(ctx, server.ID)
		if err != nil || loadedServer.OwnerID != "owner" || loadedServer.CreatedBy != "creator" || loadedServer.UpdatedBy != "editor" {
			t.Fatalf("服务器更新错误覆盖归属：%+v，%v", loadedServer.ResourceScope, err)
		}
		changed.ProjectID = "project-b"
		changedServer.ProjectID = "project-b"
		if err := domains.Update(ctx, changed); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("普通更新不应迁移域名项目：%v", err)
		}
		if err := servers.UpdateServer(ctx, changedServer); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("普通更新不应迁移服务器项目：%v", err)
		}
	})
	t.Run("project_delete_protected_without_binding", func(t *testing.T) {
		if err := projects.DeleteProject(ctx, "project-a"); err == nil {
			t.Fatal("仍被资源引用的项目被删除")
		}
		if err := projects.DeleteProject(ctx, "project-b"); err == nil {
			t.Fatal("仅被服务器引用的项目被删除")
		}
	})
	if err := servers.ReplaceDomainServers(ctx, item.ID, []string{server.ID, server.ID}); err != nil {
		t.Fatal(err)
	}
	assertBinding := func(t *testing.T) {
		t.Helper()
		items, err := servers.ListServersByDomainID(ctx, item.ID)
		if err != nil || len(items) != 1 || items[0].ID != server.ID || items[0].ProjectID != item.ProjectID {
			t.Fatalf("原部署绑定未保留：%+v，%v", items, err)
		}
	}
	for _, ids := range [][]string{{server.ID, other.ID}, {server.ID, "missing"}, {""}} {
		t.Run("binding_failure_keeps_previous", func(t *testing.T) {
			if err := servers.ReplaceDomainServers(ctx, item.ID, ids); err == nil {
				t.Fatal("无效或跨项目绑定未被拒绝")
			}
			assertBinding(t)
		})
	}
	t.Run("missing_domain_even_when_clearing", func(t *testing.T) {
		if err := servers.ReplaceDomainServers(ctx, "missing", nil); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("不存在域名的空绑定应被拒绝：%v", err)
		}
	})
	t.Run("database_constraints", func(t *testing.T) {
		statements := []struct {
			query string
			args  []any
		}{
			{"UPDATE domains SET project_id = 'missing' WHERE id = ?", []any{item.ID}},
			{"UPDATE servers SET project_id = 'missing' WHERE id = ?", []any{other.ID}},
			{"UPDATE domains SET project_id = 'project-b' WHERE id = ?", []any{item.ID}},
			{"UPDATE servers SET project_id = 'project-b' WHERE id = ?", []any{server.ID}},
			{"UPDATE domains SET id = 'renamed' WHERE id = ?", []any{item.ID}},
			{"UPDATE servers SET id = 'renamed' WHERE id = ?", []any{server.ID}},
			{"UPDATE projects SET id = 'renamed' WHERE id = 'project-a'", nil},
			{"DELETE FROM projects WHERE id = 'project-a'", nil},
			{"DELETE FROM domains WHERE id = ?", []any{item.ID}},
			{"DELETE FROM servers WHERE id = ?", []any{server.ID}},
			{"INSERT INTO domain_servers (domain_id, server_id, project_id) VALUES (?, ?, 'project-a')", []any{item.ID, other.ID}},
			{"UPDATE domain_servers SET server_id = ? WHERE domain_id = ?", []any{other.ID, item.ID}},
			{"UPDATE domain_servers SET project_id = 'project-b' WHERE domain_id = ?", []any{item.ID}},
			{"INSERT INTO domain_servers (domain_id, server_id) VALUES ('missing', ?)", []any{server.ID}},
		}
		for _, statement := range statements {
			if _, err := db.DB.ExecContext(ctx, Rebind(cfg.Driver, statement.query), statement.args...); err == nil {
				t.Errorf("直接 SQL 绕过约束：%s", statement.query)
			}
		}
		assertBinding(t)
	})
	t.Run("missing_project_create", func(t *testing.T) {
		invalid := item
		invalid.ID, invalid.Name, invalid.ProjectID = "invalid-domain", "invalid.example.test", "missing"
		if err := domains.Create(ctx, invalid); err == nil {
			t.Fatal("域名引用不存在项目")
		}
		invalidServer := server
		invalidServer.ID, invalidServer.ProjectID = "invalid-server", "missing"
		if err := servers.CreateServer(ctx, invalidServer); err == nil {
			t.Fatal("服务器引用不存在项目")
		}
	})
	t.Run("legacy_default", func(t *testing.T) {
		legacy, err := domain.NewDomain("legacy.example.test", domain.CertificateSingle, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacy.ResourceScope = domain.ResourceScope{}
		if err := domains.Create(ctx, legacy); err != nil {
			t.Fatal(err)
		}
		loaded, err := domains.GetByID(ctx, legacy.ID)
		if err != nil || loaded.ProjectID != domain.DefaultProjectID || loaded.OwnerID != "" {
			t.Fatalf("兼容调用的默认项目错误：%+v，%v", loaded.ResourceScope, err)
		}
	})
	t.Run("delete_resources_then_project", func(t *testing.T) {
		if err := domains.Delete(ctx, item.ID); err != nil {
			t.Fatal(err)
		}
		if err := projects.DeleteProject(ctx, "project-a"); err == nil {
			t.Fatal("服务器仍引用项目时不应允许删除")
		}
		if err := servers.DeleteServer(ctx, server.ID); err != nil {
			t.Fatal(err)
		}
		if err := projects.DeleteProject(ctx, "project-a"); err != nil {
			t.Fatal(err)
		}
	})
}
