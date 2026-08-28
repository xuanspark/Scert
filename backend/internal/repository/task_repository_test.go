package repository

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

func newTaskRepositoryTest(t *testing.T) (*SQLTaskRepository, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Database.DSN = filepath.Join(dir, "data", "test.db")
	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		db.Close()
		t.Fatalf("迁移测试数据库失败：%v", err)
	}
	return NewSQLTaskRepository(db.DB), db.DB
}

func TestSQLTaskRepositoryLifecycle(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	task := Task{
		ID: "task-1", DomainID: "domain-1", TaskType: "certificate_issue",
		Status: "queued", Stage: "queued", Message: "等待执行", CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.CreateTask(ctx, task); err != nil {
		t.Fatalf("创建任务失败：%v", err)
	}
	got, err := repository.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("读取任务失败：%v", err)
	}
	if got.ID != task.ID || got.Status != "queued" || got.Stage != "queued" {
		t.Fatalf("读取任务内容错误：%+v", got)
	}
	started := now.Add(time.Minute)
	if err := repository.UpdateTask(ctx, task.ID, "running", "certificate_issue", "正在申请证书", &started, nil); err != nil {
		t.Fatalf("更新任务失败：%v", err)
	}
	active, err := repository.FindActiveTaskByDomain(ctx, task.DomainID, task.TaskType)
	if err != nil || active.Status != "running" {
		t.Fatalf("查询活跃任务失败：%+v %v", active, err)
	}
	finished := started.Add(time.Minute)
	if err := repository.UpdateTask(ctx, task.ID, "success", "certificate_issue", "执行成功", &started, &finished); err != nil {
		t.Fatalf("完成任务失败：%v", err)
	}
	if _, err := repository.FindActiveTaskByDomain(ctx, task.DomainID, task.TaskType); err != sql.ErrNoRows {
		t.Fatalf("已完成任务不应被视为活跃任务：%v", err)
	}
	attempt := TaskAttempt{
		ID: "attempt-1", TaskID: task.ID, AttemptNumber: 1, Stage: "certificate_issue",
		Status: "success", Message: "执行成功", StartedAt: &started, FinishedAt: &finished, CreatedAt: finished,
	}
	if err := repository.CreateAttempt(ctx, attempt); err != nil {
		t.Fatalf("创建任务尝试失败：%v", err)
	}
	if err := repository.FinishAttempt(ctx, attempt.ID, "success", "执行成功", &finished); err != nil {
		t.Fatalf("完成任务尝试失败：%v", err)
	}
	attempts, err := repository.ListAttempts(ctx, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "success" {
		t.Fatalf("读取任务尝试失败：%+v %v", attempts, err)
	}
	items, err := repository.ListTasks(ctx, task.DomainID, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("读取任务列表失败：%+v %v", items, err)
	}
}

func TestSQLTaskRepositoryRejectsInvalidStatus(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	err := repository.CreateTask(context.Background(), Task{
		ID: "task-invalid", DomainID: "domain-1", TaskType: "certificate_issue", Status: "invalid", Stage: "queued",
	})
	if err == nil {
		t.Fatal("无效任务状态应被拒绝")
	}
}
