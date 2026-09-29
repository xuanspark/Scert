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
)

func TestProjectDomainRepositorySQLite(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, role string }{
		{"root-domain", UserRoleSuperAdmin}, {"domain-manager", UserRoleUser}, {"domain-outsider", UserRoleUser},
	} {
		if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users
			(id, username, password_hash, role, status) VALUES (?, ?, 'test-only-no-login', ?, 'active')`), user.id, user.id, user.role); err != nil {
			t.Fatal(err)
		}
	}
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	if err := projects.CreateProject(t.Context(), domain.Project{ID: "domain-project", Name: "域名项目"}); err != nil {
		t.Fatal(err)
	}
	writer := NewSQLProjectMemberRepository(db.DB, cfg.Driver)
	if err := writer.GrantProjectRole(t.Context(), "root-domain", "domain-project", "domain-manager", domain.ProjectRoleCertificateAdmin); err != nil {
		t.Fatal(err)
	}
	item, err := domain.NewDomain("managed.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = "managed-domain"
	item.ProjectID = "domain-project"
	if err := writer.CreateProjectDomain(t.Context(), "domain-manager", item); err != nil {
		t.Fatal(err)
	}
	stored, err := NewSQLDomainRepository(db.DB, cfg.Driver).GetByID(t.Context(), item.ID)
	if err != nil || stored.ProjectID != item.ProjectID {
		t.Fatalf("项目域名未按真实归属保存：%+v，%v", stored, err)
	}
	if err := writer.CreateProjectDomain(t.Context(), "domain-outsider", item); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("越权创建应拒绝：%v", err)
	}
	item.Name = "updated.example.test"
	if err := writer.UpdateProjectDomain(t.Context(), "domain-manager", item); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSQLDomainRepository(db.DB, cfg.Driver).GetByID(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}
	serverRepo := NewSQLServerRepository(db.DB, cfg.Driver)
	server := deployment.Server{
		ResourceScope:        domain.ResourceScope{ProjectID: item.ProjectID},
		ID:                   "domain-delete-server",
		Name:                 "domain delete test server",
		Address:              "192.0.2.10",
		Port:                 22,
		Username:             "deploy",
		HostKeyFingerprint:   "SHA256:test",
		PasswordRef:          "secret://ssh/domain-delete-server",
		CertificatePath:      "/etc/scert/{domain}/cert.pem",
		PrivateKeyPath:       "/etc/scert/{domain}/key.pem",
		CertificateChainPath: "/etc/scert/{domain}/fullchain.pem",
		WebServer:            deployment.WebServerNginx,
	}
	if err := serverRepo.CreateServer(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	if err := serverRepo.ReplaceDomainServers(t.Context(), item.ID, []string{server.ID}); err != nil {
		t.Fatal(err)
	}
	otherItem, err := domain.NewDomain("surviving.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	otherItem.ID = "surviving-domain"
	otherItem.ProjectID = item.ProjectID
	if err := writer.CreateProjectDomain(t.Context(), "domain-manager", otherItem); err != nil {
		t.Fatal(err)
	}
	if err := serverRepo.ReplaceDomainServers(t.Context(), otherItem.ID, []string{server.ID}); err != nil {
		t.Fatal(err)
	}
	taskRepo := NewSQLTaskRepository(db.DB, cfg.Driver)
	if err := taskRepo.CreateTask(t.Context(), Task{HistoryScope: HistoryScope{ProjectID: item.ProjectID, ScopeType: ScopeProject}, ID: "domain-active-task", DomainID: item.ID, TaskType: "certificate_issue", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.DeleteProjectDomain(t.Context(), "domain-manager", item.ID); !errors.Is(err, ErrProjectDomainBusy) {
		t.Fatalf("在途任务下删除应拒绝：%v", err)
	}
	if _, err := db.DB.ExecContext(context.Background(), Rebind(cfg.Driver, `UPDATE tasks SET status = 'success' WHERE id = ?`), "domain-active-task"); err != nil {
		t.Fatal(err)
	}
	activeStatuses := []string{"queued", "running", "retry_wait", "cancelling"}
	taskTypes := []string{"certificate_issue", "certificate_validate", "certificate_deploy", "certificate_import", "certificate_restore", "renewal_run"}
	for _, taskType := range taskTypes {
		for _, status := range activeStatuses {
			taskID := "domain-matrix-task-" + taskType + "-" + status
			if err := taskRepo.CreateTask(t.Context(), Task{HistoryScope: HistoryScope{ProjectID: item.ProjectID, ScopeType: ScopeProject}, ID: taskID, DomainID: item.ID, TaskType: taskType, Status: status}); err != nil {
				t.Fatal(err)
			}
			if err := writer.DeleteProjectDomain(t.Context(), "domain-manager", item.ID); !errors.Is(err, ErrProjectDomainBusy) {
				t.Fatalf("delete should reject for task type %s and status %s: %v", taskType, status, err)
			}
			if _, err := NewSQLDomainRepository(db.DB).GetByID(t.Context(), item.ID); err != nil {
				t.Fatalf("domain disappeared after rejected delete: %v", err)
			}
			associated, err := serverRepo.ListServersByDomainID(t.Context(), item.ID)
			if err != nil || len(associated) != 1 || associated[0].ID != server.ID {
				t.Fatalf("server binding changed after rejected delete: %+v, %v", associated, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, "UPDATE tasks SET status = 'partial_success' WHERE id = ?"), taskID); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, status := range []string{"failed", "cancelled"} {
		taskID := "domain-terminal-task-" + status
		if err := taskRepo.CreateTask(t.Context(), Task{HistoryScope: HistoryScope{ProjectID: item.ProjectID, ScopeType: ScopeProject}, ID: taskID, DomainID: item.ID, TaskType: "certificate_issue", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	trigger := `CREATE TRIGGER reject_project_domain_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit-failure'); END`
	for _, status := range []string{"retry_wait", "cancelling"} {
		taskID := "domain-active-task-" + status
		if err := taskRepo.CreateTask(t.Context(), Task{HistoryScope: HistoryScope{ProjectID: item.ProjectID, ScopeType: ScopeProject}, ID: taskID, DomainID: item.ID, TaskType: "certificate_deploy", Status: status}); err != nil {
			t.Fatal(err)
		}
		if err := writer.DeleteProjectDomain(t.Context(), "domain-manager", item.ID); !errors.Is(err, ErrProjectDomainBusy) {
			t.Fatalf("任务状态 %s 下删除应拒绝：%v", status, err)
		}
		if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, "UPDATE tasks SET status = 'partial_success' WHERE id = ?"), taskID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB.ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	if err := writer.UpdateProjectDomain(t.Context(), "domain-manager", item); err == nil {
		t.Fatal("审计失败时更新域名不应成功")
	}
	stored, err = NewSQLDomainRepository(db.DB).GetByID(t.Context(), item.ID)
	if err != nil || stored.Name != "updated.example.test" {
		t.Fatalf("审计失败后域名更新未回滚：%+v，%v", stored, err)
	}
	newItem, err := domain.NewDomain("audit-create.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	newItem.ID = "audit-create-domain"
	newItem.ProjectID = "domain-project"
	if err := writer.CreateProjectDomain(t.Context(), "domain-manager", newItem); err == nil {
		t.Fatal("审计失败时创建域名不应成功")
	}
	if _, err := NewSQLDomainRepository(db.DB).GetByID(t.Context(), newItem.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("审计失败后域名创建未回滚：%v", err)
	}
	if _, err := db.DB.ExecContext(t.Context(), `DROP TRIGGER reject_project_domain_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	if err := writer.DeleteProjectDomain(t.Context(), "domain-manager", item.ID); err == nil {
		t.Fatal("审计失败时删除域名不应成功")
	}
	if _, err := NewSQLDomainRepository(db.DB).GetByID(t.Context(), item.ID); err != nil {
		t.Fatalf("审计失败后域名删除未回滚：%v", err)
	}
	if _, err := db.DB.ExecContext(t.Context(), `DROP TRIGGER reject_project_domain_audit`); err != nil {
		t.Fatal(err)
	}
	if err := writer.DeleteProjectDomain(t.Context(), "domain-manager", item.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.DB.QueryRowContext(t.Context(), Rebind(cfg.Driver, `SELECT COUNT(*) FROM audit_logs WHERE resource_id = ? AND status = 'success'`), item.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("域名成功操作审计数量错误：%d", count)
	}
	if _, err := NewSQLDomainRepository(db.DB, cfg.Driver).GetByID(t.Context(), item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("删除后域名应不存在：%v", err)
	}

	if _, err := NewSQLDomainRepository(db.DB).GetByID(t.Context(), otherItem.ID); err != nil {
		t.Fatalf("surviving domain was removed with deleted domain: %v", err)
	}
	if _, err := serverRepo.GetServerByID(t.Context(), server.ID); err != nil {
		t.Fatalf("server was removed with deleted domain: %v", err)
	}
	associated, err := serverRepo.ListServersByDomainID(t.Context(), otherItem.ID)
	if err != nil || len(associated) != 1 || associated[0].ID != server.ID {
		t.Fatalf("surviving domain binding was removed: %+v, %v", associated, err)
	}
}
