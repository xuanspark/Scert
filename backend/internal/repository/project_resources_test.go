package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestProjectResourcesSQLite(t *testing.T) {
	checkProjectResources(t, config.Default(t.TempDir()).Database, nil)
}

func TestProjectResourcesMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkProjectResources(t, server.Database(t), server.Admin())
}

func checkProjectResources(t *testing.T, cfg config.DatabaseConfig, admin *sql.DB) {
	t.Helper()
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLProjectMemberRepository(db.DB, cfg.Driver)
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	domains := NewSQLDomainRepository(db.DB, cfg.Driver)
	servers := NewSQLServerRepository(db.DB, cfg.Driver)
	for _, user := range []struct{ id, role string }{
		{"root", "super_admin"}, {"manager", "user"}, {"cert", "user"}, {"deploy", "admin"},
		{"dns", "user"}, {"audit", "user"}, {"reader", "user"}, {"outsider", "admin"},
	} {
		if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users
			(id, username, password_hash, role, status) VALUES (?, ?, 'test-only-no-login', ?, 'active')`),
			user.id, user.id, user.role); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		if err := projects.CreateProject(t.Context(), domain.Project{ID: "project-" + id, Name: "资源项目"}); err != nil {
			t.Fatal(err)
		}
		item, err := domain.NewDomain(id+".example.test", domain.CertificateSingle, nil)
		if err != nil {
			t.Fatal(err)
		}
		item.ID, item.ProjectID = "domain-"+id, "project-"+id
		if err := domains.Create(t.Context(), item); err != nil {
			t.Fatal(err)
		}
		server := deployment.Server{
			ResourceScope: domain.ResourceScope{ProjectID: item.ProjectID},
			ID:            "server-" + id, Name: "资源服务器", Address: "192.0.2.10", Port: 22, Username: "deploy",
			PrivateKeyRef: "secret://ssh/fixture", HostKeyFingerprint: "SHA256:test",
			CertificatePath: "/ssl/cert.pem", PrivateKeyPath: "/ssl/key.pem", WebServer: deployment.WebServerNginx,
		}
		if err := servers.CreateServer(t.Context(), server); err != nil {
			t.Fatal(err)
		}
		if err := servers.ReplaceDomainServers(t.Context(), item.ID, []string{server.ID}); err != nil {
			t.Fatal(err)
		}
	}
	for actor, role := range map[string]domain.ProjectRole{
		"manager": domain.ProjectRoleAdmin, "cert": domain.ProjectRoleCertificateAdmin,
		"deploy": domain.ProjectRoleDeploymentAdmin, "dns": domain.ProjectRoleDNSAdmin,
		"audit": domain.ProjectRoleAuditViewer, "reader": domain.ProjectRoleReadOnly,
	} {
		if err := repo.GrantProjectRole(t.Context(), "root", "project-a", actor, role); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.GrantProjectRole(t.Context(), "root", "project-a", "manager", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	t.Run("lists_filter_and_deduplicate", func(t *testing.T) {
		for _, tc := range []struct {
			actor            string
			domains, servers int
		}{
			{"root", 2, 2}, {"manager", 1, 1}, {"cert", 1, 0}, {"deploy", 1, 1},
			{"dns", 0, 0}, {"audit", 0, 0}, {"reader", 1, 1}, {"outsider", 0, 0}, {"missing", 0, 0},
		} {
			domainItems, err := repo.ListVisibleDomains(t.Context(), tc.actor)
			if err != nil || domainItems == nil || len(domainItems) != tc.domains {
				t.Fatalf("域名范围错误：%s，%+v，%v", tc.actor, domainItems, err)
			}
			serverItems, err := repo.ListVisibleServers(t.Context(), tc.actor)
			if err != nil || serverItems == nil || len(serverItems) != tc.servers {
				t.Fatalf("服务器范围错误：%s，%+v，%v", tc.actor, serverItems, err)
			}
			if tc.actor != "root" {
				for _, item := range domainItems {
					if item.ProjectID != "project-a" {
						t.Fatalf("跨项目域名泄露：%+v", item)
					}
				}
				for _, item := range serverItems {
					if item.ProjectID != "project-a" {
						t.Fatalf("跨项目服务器泄露：%+v", item)
					}
				}
			}
		}
	})
	t.Run("details_and_bindings_use_real_scope", func(t *testing.T) {
		for _, id := range []string{"b", "missing", "a' OR 1=1 --"} {
			if _, err := repo.GetVisibleDomain(t.Context(), "reader", "domain-"+id); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("越权域名应统一拒绝：%s，%v", id, err)
			}
			if _, err := repo.GetVisibleServer(t.Context(), "reader", "server-"+id); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("越权服务器应统一拒绝：%s，%v", id, err)
			}
		}
		for _, actor := range []string{"root", "manager", "reader", "cert"} {
			items, err := repo.ListVisibleDomainServers(t.Context(), actor, "domain-a")
			want := 1
			if actor == "cert" {
				want = 0
			}
			if err != nil || len(items) != want || (want == 1 && items[0].ID != "server-a") {
				t.Fatalf("绑定未核对双方读取权限：%s，%+v，%v", actor, items, err)
			}
		}
		if _, err := repo.ListVisibleDomainServers(t.Context(), "reader", "domain-b"); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("越权绑定查询未拒绝：%v", err)
		}
	})
	checkProjectBindings(t, db.DB, admin, repo, cfg.Driver)
	t.Run("revocation_and_disabled_account_are_not_cached", func(t *testing.T) {
		if err := repo.RevokeProjectRole(t.Context(), "root", "project-a", "reader", domain.ProjectRoleReadOnly); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.GetVisibleDomain(t.Context(), "reader", "domain-a"); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("撤权后仍能读取：%v", err)
		}
		if _, err := db.DB.ExecContext(t.Context(), `UPDATE users SET status = 'disabled' WHERE id = 'manager'`); err != nil {
			t.Fatal(err)
		}
		for _, actor := range []string{"reader", "manager"} {
			if _, err := repo.ReplaceProjectDomainServers(t.Context(), actor, "domain-a", []string{}); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("撤权或停用后仍可修改绑定：%s，%v", actor, err)
			}
			items, err := repo.ListVisibleServers(t.Context(), actor)
			if err != nil || len(items) != 0 {
				t.Fatalf("撤权或停用后仍有资源：%s，%+v，%v", actor, items, err)
			}
		}
	})
	t.Run("canceled_and_missing_identity", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := repo.ListVisibleDomains(ctx, "root"); err == nil {
			t.Fatal("取消请求不应成功")
		}
		if _, err := repo.ListVisibleServers(t.Context(), ""); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("空身份未拒绝：%v", err)
		}
	})
}
