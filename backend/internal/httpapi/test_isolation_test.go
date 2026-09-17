package httpapi

import (
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestReadyTestConfigIsolatesRuntimePaths(t *testing.T) {
	root := t.TempDir()
	cfg := readyTestConfig(root)
	for name, path := range map[string]string{
		"install": cfg.Paths.Install, "config": cfg.Paths.Config,
		"data": cfg.Paths.Data, "log": cfg.Paths.Log,
		"certificate": cfg.Paths.Certificate, "backup": cfg.Paths.Backup,
		"database": cfg.Database.DSN,
	} {
		relative, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(relative) {
			t.Fatalf("测试路径越过临时目录：%s=%s，错误=%v", name, path, err)
		}
	}
	if !cfg.SetupReady || cfg.Database.Driver != "sqlite" {
		t.Fatal("隔离配置必须保留已初始化状态和 SQLite 数据库")
	}
}

func TestReadyTestConfigCreatesIndependentDatabases(t *testing.T) {
	for range 2 {
		cfg := readyTestConfig(t.TempDir())
		db, err := database.Open(t.Context(), cfg.Database)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := database.Migrate(t.Context(), db.DB, cfg.Database.Driver); err != nil {
			t.Fatal(err)
		}
		var sequence int
		var name, path string
		if err := db.DB.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		if filepath.Clean(path) != filepath.Clean(cfg.Database.DSN) {
			t.Fatalf("测试实际连接了错误的数据库：%s", path)
		}
		tasks := repository.NewSQLTaskRepository(db.DB)
		items, err := tasks.ListTasks(t.Context(), "", 10)
		if err != nil || len(items) != 0 {
			t.Fatalf("新测试数据库不为空：%+v，错误=%v", items, err)
		}
		if err := tasks.CreateTask(t.Context(), repository.Task{
			ID: "isolation-task", DomainID: "management-certificate",
			TaskType: "management_renewal", Status: "running", Stage: "preflight",
		}); err != nil {
			t.Fatalf("测试数据库被其他用例复用或不可写：%v", err)
		}
	}
}
