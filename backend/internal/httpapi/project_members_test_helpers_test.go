package httpapi

import (
	"database/sql"
	"net/http"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type projectMemberAPIFixture struct {
	server  *Server
	db      *sql.DB
	members *repository.SQLProjectMemberRepository
	audit   *repository.SQLAuditRepository
	cookies map[string]*http.Cookie
}

func newProjectMemberAPIFixture(t *testing.T) projectMemberAPIFixture {
	t.Helper()
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "owner"
	cfg.Admin.Email = "owner@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "FixturePass123")
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Database.Driver); err != nil {
		t.Fatal(err)
	}
	users := repository.NewSQLUserRepository(db.DB, cfg.Database.Driver)
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatal(err)
	}
	projects := repository.NewSQLProjectRepository(db.DB, cfg.Database.Driver)
	for _, id := range []string{"project-a", "project-b"} {
		if err := projects.CreateProject(t.Context(), domain.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	f := projectMemberAPIFixture{
		db: db.DB, members: repository.NewSQLProjectMemberRepository(db.DB, cfg.Database.Driver),
		audit: repository.NewSQLAuditRepository(db.DB, cfg.Database.Driver), cookies: make(map[string]*http.Cookie),
	}
	f.server = New(Dependencies{
		Config: cfg, Users: users, ProjectMembers: f.members, ProjectDirectory: f.members,
		ProjectLifecycle: f.members, ProjectResources: f.members, ProjectBindings: f.members, Audit: f.audit,
	})
	for _, account := range []struct{ id, role string }{
		{"primary-super-admin", repository.UserRoleSuperAdmin}, {"manager", repository.UserRoleUser},
		{"global-admin", repository.UserRoleAdmin}, {"viewer", repository.UserRoleUser}, {"target", repository.UserRoleUser},
	} {
		f.cookies[account.id] = httpUserSessionCookie(f.server, account.id, account.role)
	}
	for user, role := range map[string]domain.ProjectRole{"manager": domain.ProjectRoleAdmin, "viewer": domain.ProjectRoleReadOnly} {
		if err := f.members.GrantProjectRole(t.Context(), "primary-super-admin", "project-a", user, role); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f projectMemberAPIFixture) lastAudit(t *testing.T) repository.AuditLog {
	t.Helper()
	logs, err := f.audit.ListAuditLogs(t.Context(), 1)
	if err != nil || len(logs) != 1 {
		t.Fatalf("成员审计缺失：%+v，%v", logs, err)
	}
	return logs[0]
}
