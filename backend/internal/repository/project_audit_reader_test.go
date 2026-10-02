package repository

import (
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func TestProjectAuditReaderScopesLogsAndHonorsRevocation(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "project-audit.db")
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct {
		id   string
		role string
	}{
		{"root", UserRoleSuperAdmin},
		{"global-admin", UserRoleAdmin},
		{"viewer", UserRoleUser},
	} {
		if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users
			(id, username, password_hash, role, status) VALUES (?, ?, 'test-only-no-login', ?, 'active')`),
			user.id, user.id, user.role); err != nil {
			t.Fatal(err)
		}
	}
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	for _, id := range []string{"project-a", "project-b"} {
		if err := projects.CreateProject(t.Context(), domain.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	domains := NewSQLDomainRepository(db.DB, cfg.Driver)
	for _, item := range []struct {
		id        string
		projectID string
		name      string
	}{{"domain-a", "project-a", "a.example.test"}, {"domain-b", "project-b", "b.example.test"}} {
		domainItem, err := domain.NewDomain(item.name, domain.CertificateSingle, nil)
		if err != nil {
			t.Fatal(err)
		}
		domainItem.ID, domainItem.ProjectID = item.id, item.projectID
		if err := domains.Create(t.Context(), domainItem); err != nil {
			t.Fatal(err)
		}
	}
	members := NewSQLProjectMemberRepository(db.DB, cfg.Driver)
	if err := members.GrantProjectRole(t.Context(), "root", "project-a", "viewer", domain.ProjectRoleAuditViewer); err != nil {
		t.Fatal(err)
	}
	audit := NewSQLAuditRepository(db.DB, cfg.Driver)
	createdAt := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for _, item := range []AuditLog{
		{HistoryScope: HistoryScope{ScopeType: ScopeProject, ProjectID: "project-a"}, Action: "project-a", ResourceType: "domain", ResourceID: "domain-a", Status: "success", Message: "a", CreatedAt: createdAt},
		{HistoryScope: HistoryScope{ScopeType: ScopeProject, ProjectID: "project-b"}, Action: "project-b", ResourceType: "domain", ResourceID: "domain-b", Status: "success", Message: "b", CreatedAt: createdAt.Add(time.Minute)},
		{HistoryScope: HistoryScope{ScopeType: ScopeSystem}, Action: "system", ResourceType: "auth", ResourceID: "login", Status: "success", Message: "system", CreatedAt: createdAt.Add(2 * time.Minute)},
	} {
		if err := audit.CreateAuditLog(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	reader := ProjectAuditReader(members)
	items, err := reader.ListVisibleAuditLogs(t.Context(), "viewer", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("项目审计范围错误：%+v", items)
	}
	items, err = reader.ListVisibleAuditLogs(t.Context(), "global-admin", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("系统管理员审计范围错误：%+v", items)
	}
	if err := members.RevokeProjectRole(t.Context(), "root", "project-a", "viewer", domain.ProjectRoleAuditViewer); err != nil {
		t.Fatal(err)
	}
	items, err = reader.ListVisibleAuditLogs(t.Context(), "viewer", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("撤权后仍返回审计日志：%+v", items)
	}
}
