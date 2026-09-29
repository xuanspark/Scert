package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSQLTaskRepositoryRetryChecksPermissionAndCreatesAttempt(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	for _, item := range []struct {
		id   string
		role string
	}{
		{id: "retry-admin", role: "admin"},
		{id: "retry-reader", role: "user"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, status) VALUES (?, ?, 'test', ?, 'active')`, item.id, item.id, item.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role) VALUES ('default', 'retry-admin', 'project_admin'), ('default', 'retry-reader', 'read_only')`); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Add(-time.Minute)
	if err := repository.CreateTask(ctx, Task{
		ID: "task-retry", DomainID: "domain-1", TaskType: "certificate_issue", Status: "failed",
		Stage: "certificate_issue", Message: "申请失败", CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAttempt(ctx, TaskAttempt{
		ID: "attempt-retry-1", TaskID: "task-retry", AttemptNumber: 1, Stage: "certificate_issue",
		Status: "failed", Message: "申请失败", CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	if _, _, err := repository.RetryVisibleTask(ctx, "retry-reader", "task-retry", &started); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("read-only member should not retry: %v", err)
	}
	task, attempt, err := repository.RetryVisibleTask(ctx, "retry-admin", "task-retry", &started)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.OperatorID != "retry-admin" || attempt.AttemptNumber != 2 || attempt.Status != "running" || attempt.ProjectID != task.ProjectID {
		t.Fatalf("retry state is incorrect: task=%+v attempt=%+v", task, attempt)
	}
	if _, _, err := repository.RetryVisibleTask(ctx, "retry-admin", "task-retry", &started); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("running task should not be retried again: %v", err)
	}
}

func TestSQLTaskRepositoryConcurrentRetryAllowsOnlyOneWinner(t *testing.T) {
	repository, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, status) VALUES ('retry-admin', 'retry-admin', 'test', 'admin', 'active')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role) VALUES ('default', 'retry-admin', 'project_admin')`); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Add(-time.Minute)
	if err := repository.CreateTask(ctx, Task{
		ID: "task-concurrent-retry", DomainID: "domain-1", TaskType: "certificate_issue", Status: "failed",
		Stage: "certificate_issue", Message: "申请失败", CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAttempt(ctx, TaskAttempt{
		ID: "attempt-concurrent-retry-1", TaskID: "task-concurrent-retry", AttemptNumber: 1,
		Stage: "certificate_issue", Status: "failed", Message: "申请失败", CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			started := time.Now().UTC()
			_, _, err := repository.RetryVisibleTask(ctx, "retry-admin", "task-concurrent-retry", &started)
			results <- err
		}()
	}
	var successCount int
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successCount++
		} else if !errors.Is(err, ErrTaskNotRetryable) {
			t.Fatalf("unexpected concurrent retry error: %v", err)
		}
	}
	if successCount != 1 {
		t.Fatalf("expected exactly one retry winner, got %d", successCount)
	}
	attempts, err := repository.ListAttempts(ctx, "task-concurrent-retry")
	if err != nil || len(attempts) != 2 || attempts[1].AttemptNumber != 2 || attempts[1].Status != "running" {
		t.Fatalf("concurrent retry attempts are incorrect: %+v %v", attempts, err)
	}
}
