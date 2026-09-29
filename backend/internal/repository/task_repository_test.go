package repository

import (
	"context"
	"database/sql"
	"errors"
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
	for _, id := range []string{"domain-1", "domain-2", "domain-3"} {
		if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO domains (id, name, certificate_type, sans_json, status)
			VALUES (?, ?, 'single', '[]', 'pending')`, id, id+".example.test"); err != nil {
			db.Close()
			t.Fatal(err)
		}
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

func TestSQLTaskRepositoryRejectsConcurrentActiveTaskOfSameType(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	if err := repository.CreateTask(ctx, Task{
		ID: "task-active", DomainID: "domain-1", TaskType: "certificate_validate",
		Status: "running", Stage: "certificate_validate", Message: "正在校验证书",
	}); err != nil {
		t.Fatalf("创建活动任务失败：%v", err)
	}
	err := repository.CreateTask(ctx, Task{
		ID: "task-duplicate", DomainID: "domain-1", TaskType: "certificate_validate",
		Status: "queued", Stage: "queued", Message: "等待校验证书",
	})
	if !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("同类活动任务应被拒绝：%v", err)
	}
	if _, err := repository.GetTask(ctx, "task-duplicate"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("拒绝并发任务后不应留下重复任务：%v", err)
	}
}

func TestSQLTaskRepositoryRejectsConcurrentManagementRenewal(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	if err := repository.CreateTask(ctx, Task{
		ID: "management-renewal-active", DomainID: ManagementResourceID, TaskType: "management_renewal",
		Status: "running", Stage: "certificate_issue",
	}); err != nil {
		t.Fatalf("创建管理证书活动任务失败：%v", err)
	}
	err := repository.CreateTask(ctx, Task{
		ID: "management-renewal-duplicate", DomainID: ManagementResourceID, TaskType: "management_renewal",
		Status: "queued", Stage: "queued",
	})
	if !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("管理证书活动任务应被拒绝：%v", err)
	}
	if _, err := repository.GetTask(ctx, "management-renewal-duplicate"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("拒绝管理证书并发任务后不应留下重复任务：%v", err)
	}
}

func TestSQLTaskRepositoryCancelMarksRunningAttempt(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repository.CreateTask(ctx, Task{
		ID: "task-cancel", DomainID: "domain-1", TaskType: "certificate_issue",
		Status: "running", Stage: "certificate_issue", Message: "正在执行", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("创建取消测试任务失败：%v", err)
	}
	if err := repository.CreateAttempt(ctx, TaskAttempt{
		ID: "attempt-cancel", TaskID: "task-cancel", AttemptNumber: 1,
		Stage: "certificate_issue", Status: "running", Message: "正在执行", StartedAt: &now, CreatedAt: now,
	}); err != nil {
		t.Fatalf("创建取消测试尝试失败：%v", err)
	}
	finished := now.Add(time.Second)
	if err := repository.CancelTask(ctx, "task-cancel", "任务已由用户取消", &finished); err != nil {
		t.Fatalf("取消任务失败：%v", err)
	}
	task, err := repository.GetTask(ctx, "task-cancel")
	if err != nil || task.Status != "cancelled" || task.FinishedAt == nil {
		t.Fatalf("取消后的任务状态错误：%+v %v", task, err)
	}
	attempts, err := repository.ListAttempts(ctx, "task-cancel")
	if err != nil || len(attempts) != 1 || attempts[0].Status != "cancelled" || attempts[0].FinishedAt == nil {
		t.Fatalf("取消后的任务尝试状态错误：%+v %v", attempts, err)
	}
	if err := repository.FinishAttempt(ctx, "attempt-cancel", "success", "晚到的成功结果", &finished); err != nil {
		t.Fatalf("重复完成已取消尝试不应失败：%v", err)
	}
	attempts, err = repository.ListAttempts(ctx, "task-cancel")
	if err != nil || attempts[0].Status != "cancelled" {
		t.Fatalf("已取消尝试不应被晚到结果覆盖：%+v %v", attempts, err)
	}
}

func TestSQLTaskRepositoryDoesNotOverwriteCancelledTask(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repository.CreateTask(ctx, Task{
		ID: "task-state", DomainID: "domain-1", TaskType: "certificate_issue",
		Status: "running", Stage: "certificate_issue", Message: "正在执行", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("创建状态测试任务失败：%v", err)
	}
	finished := now.Add(time.Second)
	if err := repository.CancelTask(ctx, "task-state", "任务已由用户取消", &finished); err != nil {
		t.Fatalf("取消状态测试任务失败：%v", err)
	}
	if err := repository.UpdateTask(ctx, "task-state", "success", "certificate_issue", "晚到的成功结果", &now, &finished); err == nil {
		t.Fatal("已取消任务不应被更新为成功")
	}
	task, err := repository.GetTask(ctx, "task-state")
	if err != nil || task.Status != "cancelled" {
		t.Fatalf("已取消任务状态被覆盖：%+v %v", task, err)
	}
}

func TestSQLTaskRepositoryRecoversInterruptedTasksAndAttempts(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	created := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	for _, item := range []Task{
		{ID: "task-queued", DomainID: "domain-1", TaskType: "certificate_issue", Status: "queued", Stage: "queued", Message: "\u7b49\u5f85\u6267\u884c", CreatedAt: created, UpdatedAt: created},
		{ID: "task-running", DomainID: "domain-2", TaskType: "certificate_issue", Status: "running", Stage: "certificate_issue", Message: "\u6b63\u5728\u6267\u884c", CreatedAt: created, UpdatedAt: created},
		{ID: "task-success", DomainID: "domain-3", TaskType: "certificate_issue", Status: "success", Stage: "certificate_issue", Message: "\u5df2\u5b8c\u6210", CreatedAt: created, UpdatedAt: created},
	} {
		if err := repository.CreateTask(ctx, item); err != nil {
			t.Fatalf("\u521b\u5efa\u6062\u590d\u6d4b\u8bd5\u4efb\u52a1\u5931\u8d25\uff1a%v", err)
		}
	}
	if err := repository.CreateAttempt(ctx, TaskAttempt{
		ID: "attempt-running", TaskID: "task-running", AttemptNumber: 1, Stage: "certificate_issue", Status: "running", Message: "\u6b63\u5728\u6267\u884c", CreatedAt: created,
	}); err != nil {
		t.Fatalf("\u521b\u5efa\u6062\u590d\u6d4b\u8bd5\u5c1d\u8bd5\u5931\u8d25\uff1a%v", err)
	}
	if err := repository.CreateAttempt(ctx, TaskAttempt{
		ID: "attempt-success", TaskID: "task-success", AttemptNumber: 1, Stage: "certificate_issue", Status: "success", Message: "\u5df2\u5b8c\u6210", CreatedAt: created,
	}); err != nil {
		t.Fatalf("\u521b\u5efa\u5df2\u5b8c\u6210\u6d4b\u8bd5\u5c1d\u8bd5\u5931\u8d25\uff1a%v", err)
	}
	if err := repository.CreateAttempt(ctx, TaskAttempt{
		ID: "attempt-success-stale", TaskID: "task-success", AttemptNumber: 2, Stage: "certificate_issue", Status: "running", Message: "\u5386\u53f2\u5c1d\u8bd5", CreatedAt: created,
	}); err != nil {
		t.Fatalf("\u521b\u5efa\u5386\u53f2\u6d3b\u52a8\u5c1d\u8bd5\u5931\u8d25\uff1a%v", err)
	}
	finished := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	count, err := repository.RecoverInterruptedTasks(ctx, finished)
	if err != nil || count != 2 {
		t.Fatalf("\u6062\u590d\u4e2d\u65ad\u4efb\u52a1\u6570\u91cf\u9519\u8bef\uff1a%d %v", count, err)
	}
	for _, id := range []string{"task-queued", "task-running"} {
		item, getErr := repository.GetTask(ctx, id)
		if getErr != nil || item.Status != "failed" || item.Stage != "service_restart" || item.FinishedAt == nil || !item.FinishedAt.Equal(finished) {
			t.Fatalf("\u4e2d\u65ad\u4efb\u52a1\u6062\u590d\u7ed3\u679c\u9519\u8bef\uff1a%+v %v", item, getErr)
		}
	}
	completed, err := repository.GetTask(ctx, "task-success")
	if err != nil || completed.Status != "success" {
		t.Fatalf("\u5df2\u5b8c\u6210\u4efb\u52a1\u4e0d\u5e94\u88ab\u6062\u590d\u903b\u8f91\u4fee\u6539\uff1a%+v %v", completed, err)
	}
	attempts, err := repository.ListAttempts(ctx, "task-running")
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" || attempts[0].FinishedAt == nil || !attempts[0].FinishedAt.Equal(finished) {
		t.Fatalf("\u8fd0\u884c\u4e2d\u4efb\u52a1\u5c1d\u8bd5\u6062\u590d\u7ed3\u679c\u9519\u8bef\uff1a%+v %v", attempts, err)
	}
	completedAttempts, err := repository.ListAttempts(ctx, "task-success")
	if err != nil || len(completedAttempts) != 2 || completedAttempts[0].Status != "success" || completedAttempts[1].Status != "running" {
		t.Fatalf("\u5df2\u5b8c\u6210\u4efb\u52a1\u5c1d\u8bd5\u4e0d\u5e94\u88ab\u6062\u590d\u903b\u8f91\u4fee\u6539\uff1a%+v %v", completedAttempts, err)
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs WHERE action = 'task_recovery'`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u6570\u91cf\u9519\u8bef\uff1a%d %v", auditCount, err)
	}
	rows, err := db.QueryContext(ctx, `SELECT resource_id, status, task_id, scope_type, project_id FROM audit_logs WHERE action = 'task_recovery' ORDER BY resource_id`)
	if err != nil {
		t.Fatalf("\u8bfb\u53d6\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u5931\u8d25\uff1a%v", err)
	}
	defer rows.Close()
	for _, expectedID := range []string{"task-queued", "task-running"} {
		if !rows.Next() {
			t.Fatalf("\u7f3a\u5c11\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\uff1a%s", expectedID)
		}
		var resourceID, status, taskID, scopeType, projectID string
		if err := rows.Scan(&resourceID, &status, &taskID, &scopeType, &projectID); err != nil {
			t.Fatalf("\u8bfb\u53d6\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u5931\u8d25\uff1a%v", err)
		}
		if resourceID != expectedID || status != "failed" || taskID != expectedID || scopeType != "project" {
			t.Fatalf("\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u5185\u5bb9\u9519\u8bef：id=%s status=%s task=%s scope=%s project=%s", resourceID, status, taskID, scopeType, projectID)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("\u8bfb\u53d6\u4e2d\u65ad\u4efb\u52a1\u5ba1\u8ba1\u6d41\u5931\u8d25\uff1a%v", err)
	}
	count, err = repository.RecoverInterruptedTasks(ctx, finished.Add(time.Minute))
	if err != nil || count != 0 {
		t.Fatalf("\u670d\u52a1\u91cd\u542f\u6062\u590d\u4e0d\u5e94\u91cd\u590d\u6267\u884c\uff1a%d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs WHERE action = 'task_recovery'`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("\u91cd\u590d\u6062\u590d\u4e0d\u5e94\u65b0\u589e\u5ba1\u8ba1\uff1a%d %v", auditCount, err)
	}
}

func TestSQLTaskRepositoryRecoveryAuditFailureRollsBack(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	if err := repository.CreateTask(ctx, Task{
		ID: "task-recovery-audit-failure", DomainID: "domain-1", TaskType: "certificate_issue",
		Status: "running", Stage: "certificate_issue", Message: "\u6b63\u5728\u6267\u884c",
	}); err != nil {
		t.Fatalf("\u521b\u5efa\u5ba1\u8ba1\u56de\u6eda\u6d4b\u8bd5\u4efb\u52a1\u5931\u8d25\uff1a%v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_task_recovery_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit-failure'); END`); err != nil {
		t.Fatalf("\u521b\u5efa\u5ba1\u8ba1\u5931\u8d25\u89e6\u53d1\u5668\u5931\u8d25\uff1a%v", err)
	}
	defer db.ExecContext(context.Background(), `DROP TRIGGER reject_task_recovery_audit`)
	if count, err := repository.RecoverInterruptedTasks(ctx, time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)); err == nil || count != 0 {
		t.Fatalf("\u5ba1\u8ba1\u5931\u8d25\u65f6\u5e94\u56de\u6eda\u6062\u590d\uff1a%d %v", count, err)
	}
	item, err := repository.GetTask(ctx, "task-recovery-audit-failure")
	if err != nil || item.Status != "running" || item.Stage != "certificate_issue" {
		t.Fatalf("\u5ba1\u8ba1\u5931\u8d25\u540e\u4efb\u52a1\u72b6\u6001\u672a\u56de\u6eda\uff1a%+v %v", item, err)
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&auditCount); err != nil || auditCount != 0 {
		t.Fatalf("\u5ba1\u8ba1\u5931\u8d25\u540e\u4e0d\u5e94\u4fdd\u7559\u65e5\u5fd7\uff1a%d %v", auditCount, err)
	}
}

func TestSQLTaskRepositoryRejectsConcurrentDuplicateIdempotencyKeys(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()

	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, taskID := range []string{"task-idempotency-a", "task-idempotency-b"} {
		go func(taskID string) {
			<-start
			results <- repository.CreateTask(ctx, Task{
				ID: taskID, DomainID: "domain-1", TaskType: "certificate_issue",
				Status: "queued", Stage: "queued", Message: "test",
				IdempotencyKey: "duplicate-key-20260929",
			})
		}(taskID)
	}
	close(start)

	var succeeded, failed int
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else {
			failed++
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("unexpected concurrent idempotency result: success=%d failure=%d", succeeded, failed)
	}

	items, err := repository.ListTasks(ctx, "domain-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].IdempotencyKey != "duplicate-key-20260929" {
		t.Fatalf("duplicate idempotency key created multiple tasks: %+v", items)
	}
	stored, err := repository.FindTaskByIdempotencyKey(ctx, "domain-1", "duplicate-key-20260929")
	if err != nil || stored.ID != items[0].ID {
		t.Fatalf("idempotency lookup mismatch: %+v %v", stored, err)
	}
}
