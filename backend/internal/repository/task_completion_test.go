package repository

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func seedTaskCompletion(t *testing.T, repo *SQLTaskRepository) TaskCompletion {
	t.Helper()
	started := time.Now().UTC().Add(-time.Minute)
	if err := repo.CreateTask(t.Context(), Task{
		ID: "completion-task", DomainID: "domain-1", TaskType: "certificate_deploy",
		Status: "running", Stage: "deployment", StartedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateAttempt(t.Context(), TaskAttempt{
		ID: "completion-attempt", TaskID: "completion-task", AttemptNumber: 1,
		Status: "running", Stage: "deployment", StartedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	return TaskCompletion{
		TaskID: "completion-task", AttemptID: "completion-attempt", Status: "success",
		Stage: "deployment", Message: "部署完成", FinishedAt: time.Now().UTC(),
	}
}

func TestTaskCompletionPersistsBothTerminalRecords(t *testing.T) {
	for _, status := range []string{"success", "failed", "cancelled", "partial_success"} {
		t.Run(status, func(t *testing.T) {
			repo, db := newTaskRepositoryTest(t)
			defer db.Close()
			completion := seedTaskCompletion(t, repo)
			completion.Status = status
			if err := repo.FinishTaskAttempt(t.Context(), completion); err != nil {
				t.Fatal(err)
			}
			task, err := repo.GetTask(t.Context(), completion.TaskID)
			if err != nil || task.Status != status || task.StartedAt == nil || task.FinishedAt == nil || task.Message != completion.Message {
				t.Fatalf("任务终态未完整保存：%+v %v", task, err)
			}
			attempts, err := repo.ListAttempts(t.Context(), completion.TaskID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != status || attempts[0].FinishedAt == nil || !attempts[0].FinishedAt.Equal(*task.FinishedAt) || attempts[0].Message != task.Message {
				t.Fatalf("任务和尝试终态不一致：%+v %v", attempts, err)
			}
			if err := repo.FinishTaskAttempt(t.Context(), completion); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("终态不能被迟到的收尾重复覆盖：%v", err)
			}
		})
	}
}

func TestTaskCompletionRollsBackWhenAttemptCannotFinish(t *testing.T) {
	for _, failure := range []string{"write_failure", "missing_attempt", "foreign_attempt", "stale_attempt", "invalid_status"} {
		t.Run(failure, func(t *testing.T) {
			repo, db := newTaskRepositoryTest(t)
			defer db.Close()
			completion := seedTaskCompletion(t, repo)
			wantAttemptStatus := "running"
			switch failure {
			case "write_failure":
				if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_attempt_completion BEFORE UPDATE ON task_attempts BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "missing_attempt":
				completion.AttemptID = "missing"
			case "foreign_attempt":
				if err := repo.CreateTask(t.Context(), Task{ID: "foreign-task", DomainID: "domain-2", TaskType: "certificate_deploy", Status: "running", Stage: "deployment"}); err != nil {
					t.Fatal(err)
				}
				if err := repo.CreateAttempt(t.Context(), TaskAttempt{ID: "foreign-attempt", TaskID: "foreign-task", AttemptNumber: 1, Status: "running", Stage: "deployment"}); err != nil {
					t.Fatal(err)
				}
				completion.AttemptID = "foreign-attempt"
			case "stale_attempt":
				if err := repo.FinishAttempt(t.Context(), completion.AttemptID, "failed", "此前失败", &completion.FinishedAt); err != nil {
					t.Fatal(err)
				}
				wantAttemptStatus = "failed"
			case "invalid_status":
				completion.Status = "running"
			}
			if err := repo.FinishTaskAttempt(t.Context(), completion); err == nil {
				t.Fatal("应拒绝无法同时关闭任务和尝试的操作")
			}
			task, err := repo.GetTask(t.Context(), completion.TaskID)
			if err != nil || task.Status != "running" || task.FinishedAt != nil {
				t.Fatalf("尝试保存失败后任务未回滚：%+v %v", task, err)
			}
			attempts, err := repo.ListAttempts(t.Context(), completion.TaskID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != wantAttemptStatus {
				t.Fatalf("失败操作修改了尝试：%+v %v", attempts, err)
			}
		})
	}
}
