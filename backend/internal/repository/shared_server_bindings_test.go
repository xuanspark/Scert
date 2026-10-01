package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// 由已有 SQLite 和隔离 MySQL 真实仓储入口调用，不使用业务库或额外验收脚本。
func checkManyToManyBindings(t *testing.T, db, admin *sql.DB, members *SQLProjectMemberRepository, driver string) {
	t.Helper()
	domains := NewSQLDomainRepository(db, driver)
	servers := NewSQLServerRepository(db, driver)
	for _, suffix := range []string{"one", "two"} {
		item, err := domain.NewDomain(suffix+".shared.example.test", domain.CertificateSingle, nil)
		if err != nil {
			t.Fatal(err)
		}
		item.ID, item.ProjectID = "shared-domain-"+suffix, "project-a"
		if err := domains.Create(t.Context(), item); err != nil {
			t.Fatal(err)
		}
		server := deployment.Server{
			ResourceScope: domain.ResourceScope{ProjectID: "project-a"},
			ID:            "shared-server-" + suffix, Name: suffix, Address: "192.0.2.20", Port: 22, Username: "deploy",
			AuthMethod: deployment.SSHAuthPassword, PasswordRef: "secret://ssh/password/fixture", HostKeyFingerprint: "SHA256:test", WebServer: deployment.WebServerNginx,
		}
		if err := servers.CreateServer(t.Context(), server); err != nil {
			t.Fatal(err)
		}
	}
	both := []string{"shared-server-one", "shared-server-two"}
	assertBindings := func(id string, expected []string) {
		t.Helper()
		items, err := members.ListVisibleDomainServers(t.Context(), "manager", id)
		if err != nil {
			t.Fatal(err)
		}
		actual := make([]string, 0, len(items))
		for _, item := range items {
			actual = append(actual, item.ID)
		}
		slices.Sort(actual)
		if !slices.Equal(actual, expected) {
			t.Fatalf("域名关联错误：%s，实际 %v，期望 %v", id, actual, expected)
		}
	}
	bind := func(id string, ids []string) {
		t.Helper()
		if _, err := members.ReplaceProjectDomainServers(t.Context(), "manager", id, ids); err != nil {
			t.Fatal(err)
		}
	}
	bind("shared-domain-one", append(slices.Clone(both), both[0]))
	bind("shared-domain-two", both)
	assertBindings("shared-domain-one", both)
	assertBindings("shared-domain-two", both)
	bind("shared-domain-one", both[:1])
	assertBindings("shared-domain-one", both[:1])
	assertBindings("shared-domain-two", both)
	bind("shared-domain-one", []string{})
	assertBindings("shared-domain-one", []string{})
	assertBindings("shared-domain-two", both)
	bind("shared-domain-one", both)
	if _, err := members.ReplaceProjectDomainServers(t.Context(), "manager", "shared-domain-one", []string{"server-b"}); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("跨项目绑定未拒绝：%v", err)
	}
	assertBindings("shared-domain-one", both)
	assertBindings("shared-domain-two", both)
	first, err := servers.ListDeploymentServersByDomainID(t.Context(), "shared-domain-one")
	if err != nil || len(first) != 2 {
		t.Fatalf("一个域名未取得两台部署服务器：%v，%v", first, err)
	}
	second, err := servers.ListDeploymentServersByDomainID(t.Context(), "shared-domain-two")
	if err != nil || len(second) != 2 {
		t.Fatalf("共享服务器读取失败：%v，%v", second, err)
	}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].CertificatePath == second[i].CertificatePath || first[i].PrivateKeyPath == second[i].PrivateKeyPath {
			t.Fatal("同一服务器的不同域名未分离文件路径")
		}
	}
	server, err := servers.GetServerByID(t.Context(), both[0])
	if err != nil {
		t.Fatal(err)
	}
	server.PrivateKeyPath = "/ssl/shared/key.pem"
	if err := servers.UpdateServer(t.Context(), server); !errors.Is(err, deployment.ErrSharedPathConflict) {
		t.Fatalf("共享服务器改为固定私钥路径未拒绝：%v", err)
	}
	stored, err := servers.GetServerByID(t.Context(), both[0])
	if err != nil || stored.PrivateKeyPath != deployment.DefaultPrivateKeyPath {
		t.Fatalf("被拒绝的配置仍写入：%+v，%v", stored, err)
	}
	// 模拟旧数据库已有的危险配置，部署前和新关联写入必须再次拒绝。
	if _, err := db.ExecContext(t.Context(), Rebind(driver, `UPDATE servers SET private_key_path='/ssl/shared/key.pem' WHERE id=?`), both[0]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"shared-domain-one", "shared-domain-two"} {
		if _, err := servers.ListDeploymentServersByDomainID(t.Context(), id); !errors.Is(err, deployment.ErrSharedPathConflict) {
			t.Fatalf("历史冲突仍允许部署：%s，%v", id, err)
		}
	}
	if _, err := members.ReplaceProjectDomainServers(t.Context(), "manager", "shared-domain-one", both); !errors.Is(err, deployment.ErrSharedPathConflict) {
		t.Fatalf("危险路径仍可写入关联：%v", err)
	}
	if err := servers.ReplaceDomainServers(t.Context(), "shared-domain-one", both); !errors.Is(err, deployment.ErrSharedPathConflict) {
		t.Fatalf("旧仓储入口绕过路径保护：%v", err)
	}
	assertBindings("shared-domain-one", both)
	assertBindings("shared-domain-two", both)
	if err := servers.UpdateServer(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	// 两个域名均保留绑定时，审计故障不得仅回滚其中一部分。
	executor := db
	triggerName := "reject_shared_binding_audit"
	trigger := `CREATE TRIGGER reject_shared_binding_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit-failure'); END`
	if driver == "mysql" {
		var schema string
		if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
		triggerName = schema + ".reject_shared_binding_audit"
		trigger = "CREATE TRIGGER " + triggerName + " BEFORE INSERT ON " + schema + ".audit_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='audit-failure'"
		executor = admin
	}
	if _, err := executor.ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := executor.ExecContext(context.Background(), "DROP TRIGGER "+triggerName); err != nil {
			t.Error(err)
		}
	})
	if _, err := members.ReplaceProjectDomainServers(t.Context(), "manager", "shared-domain-one", []string{}); err == nil {
		t.Fatal("审计失败仍清空了共享关联")
	}
	assertBindings("shared-domain-one", both)
	assertBindings("shared-domain-two", both)
}
