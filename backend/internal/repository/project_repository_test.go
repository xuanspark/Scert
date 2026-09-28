package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestSQLProjectRepositorySQLite(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "projects.db")
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	checkProjectRepository(t, NewSQLProjectRepository(db.DB))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	items, err := NewSQLProjectRepository(db.DB).ListProjects(t.Context())
	if err != nil || len(items) != 2 || items[0].Name != "调整后的默认项目" || items[1].ID != "retained" {
		t.Fatalf("关闭重开后项目未保留：%+v，%v", items, err)
	}
}

func TestSQLProjectRepositoryMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	cfg := server.Database(t)
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal("打开独立 MySQL 项目测试库失败")
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	checkProjectRepository(t, NewSQLProjectRepository(db.DB, cfg.Driver))
}

func checkProjectRepository(t *testing.T, repo *SQLProjectRepository) {
	t.Helper()
	ctx := t.Context()
	items, err := repo.ListProjects(ctx)
	if err != nil || len(items) != 1 || items[0].ID != domain.DefaultProjectID || items[0].Name != "默认项目" {
		t.Fatalf("默认项目缺失：%+v，%v", items, err)
	}
	initial := items[0]
	t.Run("reserved_default", func(t *testing.T) {
		if err := repo.CreateProject(ctx, initial); !errors.Is(err, ErrDefaultProject) {
			t.Fatalf("未拒绝重复创建默认项目：%v", err)
		}
		if err := repo.DeleteProject(ctx, " default "); !errors.Is(err, ErrDefaultProject) {
			t.Fatalf("未阻止删除默认项目：%v", err)
		}
	})
	item := domain.Project{ID: "project-a", Name: "  证书项目🔒  ", Description: "  项目说明\n第二行  "}
	item.CreatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Run("create_and_read", func(t *testing.T) {
		if err := repo.CreateProject(ctx, item); err != nil {
			t.Fatal(err)
		}
		loaded, err := repo.GetProject(ctx, " project-a ")
		if err != nil || loaded.Name != strings.TrimSpace(item.Name) || loaded.Description != strings.TrimSpace(item.Description) {
			t.Fatalf("项目数据读取不符：%+v，%v", loaded, err)
		}
		if loaded.CreatedAt.Equal(item.CreatedAt) || loaded.CreatedAt.IsZero() || !loaded.CreatedAt.Equal(loaded.UpdatedAt) {
			t.Fatal("仓储必须生成自己的创建和更新时间")
		}
		item = loaded
	})
	t.Run("duplicate_and_invalid", func(t *testing.T) {
		if err := repo.CreateProject(ctx, item); err == nil {
			t.Fatal("重复项目主键未拒绝")
		}
		bad := item
		bad.Name = " "
		if err := repo.CreateProject(ctx, bad); err == nil {
			t.Fatal("创建未校验项目")
		}
		if err := repo.UpdateProject(ctx, bad); err == nil {
			t.Fatal("更新未校验项目")
		}
	})
	t.Run("update_and_order", func(t *testing.T) {
		before, err := repo.GetProject(ctx, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		item.Name = "更新后的项目"
		item.CreatedAt = time.Time{}
		for round := 0; round < 2; round++ {
			if err := repo.UpdateProject(ctx, item); err != nil {
				t.Fatal(err)
			}
		}
		loaded, err := repo.GetProject(ctx, item.ID)
		if err != nil || loaded.Name != item.Name || !loaded.CreatedAt.Equal(before.CreatedAt) || loaded.UpdatedAt.Before(before.UpdatedAt) {
			t.Fatalf("更新改变了创建时间或丢失内容：%+v，%v", loaded, err)
		}
		if err := repo.CreateProject(ctx, domain.Project{ID: "retained", Name: item.Name}); err != nil {
			t.Fatal(err)
		}
		items, err := repo.ListProjects(ctx)
		if err != nil || len(items) != 3 || items[0].ID != domain.DefaultProjectID {
			t.Fatalf("项目列表必须默认项目在前：%+v，%v", items, err)
		}
		initial.Name = "调整后的默认项目"
		if err := repo.UpdateProject(ctx, initial); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancelled_context", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := repo.CreateProject(cancelled, domain.Project{ID: "cancelled", Name: "不应写入"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后创建未被拒绝：%v", err)
		}
		if _, err := repo.ListProjects(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后查询未被拒绝：%v", err)
		}
		if err := repo.DeleteProject(cancelled, item.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后删除未被拒绝：%v", err)
		}
	})
	t.Run("missing_and_injection", func(t *testing.T) {
		for _, id := range []string{"missing", "' OR 1=1 --"} {
			if _, err := repo.GetProject(ctx, id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("查询不存在项目应保留未找到语义：%v", err)
			}
			if err := repo.DeleteProject(ctx, id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("删除不存在项目应保留未找到语义：%v", err)
			}
		}
		if err := repo.UpdateProject(ctx, domain.Project{ID: "missing", Name: "不存在"}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("更新不存在项目应保留未找到语义：%v", err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		if err := repo.DeleteProject(ctx, item.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.GetProject(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("删除后仍能读取项目：%v", err)
		}
		items, err := repo.ListProjects(ctx)
		if err != nil || len(items) != 2 || items[1].ID != "retained" {
			t.Fatalf("删除项目影响了其他项目：%+v，%v", items, err)
		}
	})
}

func TestSQLProjectRepositoryNilDatabase(t *testing.T) {
	for _, repo := range []*SQLProjectRepository{nil, NewSQLProjectRepository(nil)} {
		if err := repo.CreateProject(t.Context(), domain.Project{}); err == nil {
			t.Fatal("创建没有拒绝空数据库")
		}
		if _, err := repo.GetProject(t.Context(), "a"); err == nil {
			t.Fatal("查询没有拒绝空数据库")
		}
		if _, err := repo.ListProjects(t.Context()); err == nil {
			t.Fatal("列表没有拒绝空数据库")
		}
		if err := repo.UpdateProject(t.Context(), domain.Project{}); err == nil {
			t.Fatal("更新没有拒绝空数据库")
		}
		if err := repo.DeleteProject(t.Context(), "a"); err == nil {
			t.Fatal("删除没有拒绝空数据库")
		}
	}
}
