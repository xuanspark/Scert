package repository

import (
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestTaskCompletionRetryKeepsTaskActiveAndPreservesReport(t *testing.T) {
	repo, db := newTaskRepositoryTest(t)
	defer db.Close()
	completion := seedTaskCompletion(t, repo)
	completion.Status, completion.Retry = "failed", true
	completion.Report = &deployment.Report{Status: "failed", RecoveryStatus: "success"}
	if err := repo.FinishTaskAttempt(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetTask(t.Context(), completion.TaskID)
	if err != nil || task.Status != "retry_wait" || task.Stage != "retry_wait" || task.FinishedAt != nil {
		t.Fatalf("重试不能提前关闭任务：%+v %v", task, err)
	}
	attempts, err := repo.ListAttempts(t.Context(), completion.TaskID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" || attempts[0].FinishedAt == nil {
		t.Fatalf("失败尝试未收尾：%+v %v", attempts, err)
	}
	reports, err := repo.ListTaskDeploymentReports(t.Context(), completion.TaskID)
	if err != nil || len(reports) != 1 || reports[0].Report.RecoveryStatus != "success" {
		t.Fatalf("失败尝试报告未保存：%+v %v", reports, err)
	}
	// 重试等待期间取消：只关闭任务和审计，不覆盖已完成的尝试及报告。
	completion.AttemptID, completion.Status = "", "cancelled"
	completion.Retry, completion.WithoutAttempt = false, true
	completion.Report, completion.AuditAction = nil, "certificate_deploy"
	if err := repo.FinishTaskAttempt(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	task, err = repo.GetTask(t.Context(), completion.TaskID)
	if err != nil || task.Status != "cancelled" || task.FinishedAt == nil {
		t.Fatalf("等待取消未收尾：%+v %v", task, err)
	}
	attempts, err = repo.ListAttempts(t.Context(), completion.TaskID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" {
		t.Fatalf("等待取消覆盖历史尝试：%+v %v", attempts, err)
	}
}

func TestTaskCompletionRejectsUnsafeRetryAndMissingAttempt(t *testing.T) {
	for _, scenario := range []string{"active_attempt", "retry_cancelled", "retry_success", "retry_audit", "no_attempt_success", "no_attempt_report"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db := newTaskRepositoryTest(t)
			defer db.Close()
			completion := seedTaskCompletion(t, repo)
			completion.Status = "failed"
			wantStatus := "running"
			switch scenario {
			case "active_attempt":
				completion.AttemptID, completion.WithoutAttempt = "", true
			case "retry_cancelled":
				completion.Retry = true
				if err := repo.UpdateTask(t.Context(), completion.TaskID, "cancelling", "deployment", "正在取消", nil, nil); err != nil {
					t.Fatal(err)
				}
				wantStatus = "cancelling"
			case "retry_success":
				completion.Retry, completion.Status = true, "success"
			case "retry_audit":
				completion.Retry, completion.AuditAction = true, "certificate_deploy"
			case "no_attempt_success":
				completion.AttemptID, completion.WithoutAttempt, completion.Status = "", true, "success"
			case "no_attempt_report":
				completion.AttemptID, completion.WithoutAttempt = "", true
				completion.Report = &deployment.Report{}
			}
			if err := repo.FinishTaskAttempt(t.Context(), completion); err == nil {
				t.Fatal("不安全收尾未被拒绝")
			}
			task, err := repo.GetTask(t.Context(), completion.TaskID)
			if err != nil || task.Status != wantStatus || task.FinishedAt != nil {
				t.Fatalf("拒绝后改变任务状态：%+v %v", task, err)
			}
		})
	}
}

func TestTaskCompletionWithoutAttemptRollsBackOnAuditFailure(t *testing.T) {
	repo, db := newTaskRepositoryTest(t)
	defer db.Close()
	if err := repo.CreateTask(t.Context(), Task{ID: "before-attempt", DomainID: "domain-1", TaskType: "certificate_deploy", Status: "running", Stage: "deployment"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_completion_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishTaskAttempt(t.Context(), TaskCompletion{TaskID: "before-attempt", Status: "failed", Stage: "deployment", FinishedAt: time.Now().UTC(), WithoutAttempt: true, AuditAction: "certificate_deploy"}); err == nil {
		t.Fatal("审计写入失败没有返回")
	}
	task, err := repo.GetTask(t.Context(), "before-attempt")
	if err != nil || task.Status != "running" || task.FinishedAt != nil {
		t.Fatalf("无尝试路径未整体回滚：%+v %v", task, err)
	}
}
