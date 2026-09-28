package repository

import (
	"errors"
	"fmt"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestProjectDirectorySQLite(t *testing.T) {
	checkProjectDirectory(t, config.Default(t.TempDir()).Database)
}

func TestProjectDirectoryMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkProjectDirectory(t, server.Database(t))
}

func checkProjectDirectory(t *testing.T, cfg config.DatabaseConfig) {
	t.Helper()
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, role string }{
		{"root", "super_admin"}, {"manager", "user"}, {"global-admin", "admin"}, {"viewer", "user"},
	} {
		if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, `INSERT INTO users (id, username, password_hash, role, status)
			VALUES (?, ?, 'test-only-no-login', ?, 'active')`), user.id, user.id, user.role); err != nil {
			t.Fatal(err)
		}
	}
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	repo := NewSQLProjectMemberRepository(db.DB, cfg.Driver)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("visible-%02d", i)
		if err := projects.CreateProject(t.Context(), domain.Project{ID: id, Name: "测试项目", Description: "项目说明"}); err != nil {
			t.Fatal(err)
		}
		for _, role := range []domain.ProjectRole{domain.ProjectRoleAdmin, domain.ProjectRoleReadOnly} {
			if err := repo.GrantProjectRole(t.Context(), "root", id, "manager", role); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repo.GrantProjectRole(t.Context(), "root", "visible-00", "viewer", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	t.Run("visible_roles_and_empty_directory", func(t *testing.T) {
		for _, tc := range []struct {
			actor  string
			count  int
			manage bool
		}{
			{"root", 13, true}, {"manager", 12, true}, {"viewer", 1, false}, {"global-admin", 0, false},
		} {
			page, err := repo.ListVisibleProjects(t.Context(), tc.actor, "", 30)
			if err != nil || page.Items == nil || len(page.Items) != tc.count || page.HasMore || page.NextCursor != "" {
				t.Fatalf("项目可见性错误：%s，%+v，%v", tc.actor, page, err)
			}
			for _, item := range page.Items {
				if item.CanManageMembers != tc.manage || (tc.actor != "root" && item.ID == domain.DefaultProjectID) {
					t.Fatalf("全局身份被错误映射为项目权限：%s，%+v", tc.actor, item)
				}
			}
		}
	})
	t.Run("pagination_deduplicates_roles", func(t *testing.T) {
		for _, size := range []int{1, 5, 10, 15, 30} {
			after := ""
			seen := make(map[string]bool)
			for attempts := 0; ; attempts++ {
				if attempts > 12 {
					t.Fatal("项目分页游标没有前进")
				}
				page, err := repo.ListVisibleProjects(t.Context(), "manager", after, size)
				if err != nil || len(page.Items) > size || page.PageSize != size {
					t.Fatalf("项目分页无效：%+v，%v", page, err)
				}
				for _, item := range page.Items {
					if seen[item.ID] || item.ID <= after {
						t.Fatalf("项目重复或排序错误：%+v", item)
					}
					seen[item.ID] = true
				}
				if !page.HasMore {
					break
				}
				if page.NextCursor != page.Items[len(page.Items)-1].ID {
					t.Fatal("下一页游标不是最后项目")
				}
				after = page.NextCursor
			}
			if len(seen) != 12 {
				t.Fatalf("项目被截断或泄露：%d", len(seen))
			}
		}
		page, err := repo.ListVisibleProjects(t.Context(), "manager", "zzzz", 5)
		if err != nil || page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("有效空页被拒绝：%+v，%v", page, err)
		}
	})
	t.Run("realtime_revocation_and_disabled_accounts", func(t *testing.T) {
		if err := repo.RevokeProjectRole(t.Context(), "root", "visible-00", "manager", domain.ProjectRoleAdmin); err != nil {
			t.Fatal(err)
		}
		page, err := repo.ListVisibleProjects(t.Context(), "manager", "", 5)
		if err != nil || len(page.Items) == 0 || page.Items[0].CanManageMembers {
			t.Fatalf("撤销管理角色未生效：%+v，%v", page, err)
		}
		if err := repo.RevokeProjectRole(t.Context(), "root", "visible-00", "manager", domain.ProjectRoleReadOnly); err != nil {
			t.Fatal(err)
		}
		page, err = repo.ListVisibleProjects(t.Context(), "manager", "", 5)
		if err != nil || len(page.Items) == 0 || page.Items[0].ID == "visible-00" {
			t.Fatalf("已离开项目仍可见：%+v，%v", page, err)
		}
		for _, actor := range []string{"manager", "root"} {
			if _, err := db.DB.ExecContext(t.Context(), Rebind(cfg.Driver, "UPDATE users SET status='disabled' WHERE id=?"), actor); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.ListVisibleProjects(t.Context(), actor, "", 5); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("停用账户仍有权限：%v", err)
			}
		}
		for _, actor := range []string{"", "missing"} {
			if _, err := repo.ListVisibleProjects(t.Context(), actor, "", 5); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("不存在身份没有被拒绝：%v", err)
			}
		}
	})
	t.Run("invalid_limits_and_storage", func(t *testing.T) {
		for _, size := range []int{0, -1, 101} {
			if _, err := repo.ListVisibleProjects(t.Context(), "viewer", "", size); err == nil {
				t.Fatal("接受无效分页")
			}
		}
		if _, err := NewSQLProjectMemberRepository(nil).ListVisibleProjects(t.Context(), "viewer", "", 5); err == nil {
			t.Fatal("空数据库被视为成功")
		}
	})
}
