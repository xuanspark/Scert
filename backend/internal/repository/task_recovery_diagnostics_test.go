package repository

import (
	"strings"
	"testing"
	"time"
)

func TestTaskRecoveryRequiresResourceInspectionBeforeRetry(t *testing.T) {
	repo, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := t.Context()
	if err := repo.CreateTask(ctx, Task{ID: "interrupted-deploy", DomainID: "domain-1", TaskType: "certificate_deploy", Status: "running", Stage: "certificate_deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateAttempt(ctx, TaskAttempt{ID: "interrupted-attempt", TaskID: "interrupted-deploy", AttemptNumber: 1, Stage: "certificate_deploy", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.RecoverInterruptedTasks(ctx, time.Now()); err != nil || count != 1 {
		t.Fatalf("恢复任务失败：%d %v", count, err)
	}
	task, err := repo.GetTask(ctx, "interrupted-deploy")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"执行结果未确认", "远端文件", "部署锁", "服务状态", "确认安全后再重试"} {
		if !strings.Contains(task.Message, expected) {
			t.Errorf("中断任务缺少恢复边界提示 %q：%s", expected, task.Message)
		}
	}
	attempts, err := repo.ListAttempts(ctx, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Message != task.Message {
		t.Fatalf("执行尝试与任务恢复说明不一致：%+v %v", attempts, err)
	}
	var auditMessage string
	if err := db.QueryRowContext(ctx, `SELECT message FROM audit_logs WHERE action = 'task_recovery' AND task_id = ?`, task.ID).Scan(&auditMessage); err != nil || auditMessage != task.Message {
		t.Fatalf("恢复审计说明不一致：%s %v", auditMessage, err)
	}
}
