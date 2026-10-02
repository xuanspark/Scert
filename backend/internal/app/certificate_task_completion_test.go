package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestCertificateCompletionPersistsFullReportsAcrossRetries(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	calls := 0
	_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 2, time.Nanosecond,
		func(context.Context, string) (CertificateWorkflowResult, error) {
			calls++
			result := CertificateWorkflowResult{Deployment: deployment.Report{
				Status: "success", RecoveryStatus: "success", Total: 1, Success: 1,
				Results: []deployment.Result{{ServerID: "target", Status: "success"}},
			}}
			if calls == 1 {
				result.Deployment.Status = "failed"
				return result, errors.New("temporary failure")
			}
			return result, nil
		})
	if err != nil || calls != 2 {
		t.Fatalf("重试执行失败：%d %v", calls, err)
	}
	task := assertCertificateTask(t, a, "success", "failed", "success")
	reports, err := a.Tasks.ListTaskDeploymentReports(t.Context(), task.ID)
	if err != nil || len(reports) != 2 {
		t.Fatalf("每次尝试均应保存完整报告：%+v %v", reports, err)
	}
	if reports[0].Report.Status != "failed" || reports[1].Report.Status != "success" || reports[0].AttemptID == reports[1].AttemptID || reports[1].Report.RecoveryStatus != "success" {
		t.Fatalf("重试覆盖报告或丢失恢复状态：%+v", reports)
	}
}

type cancelAfterCertificateRetry struct {
	repository.TaskRepository
	cancel context.CancelFunc
}

func (r cancelAfterCertificateRetry) FinishTaskAttempt(ctx context.Context, completion repository.TaskCompletion) error {
	err := r.TaskRepository.FinishTaskAttempt(ctx, completion)
	if err == nil && completion.Retry {
		r.cancel()
	}
	return err
}

func TestCertificateCompletionCancellationDuringRetryWait(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w.tasks = cancelAfterCertificateRetry{TaskRepository: a.Tasks, cancel: cancel}
	calls := 0
	_, err := w.run(ctx, "task-domain", "renewal_run", "certificate_issue", 3, time.Hour,
		func(context.Context, string) (CertificateWorkflowResult, error) {
			calls++
			return CertificateWorkflowResult{}, errors.New("temporary failure")
		})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("等待取消后继续重试或丢失取消原因：%d %v", calls, err)
	}
	task := assertCertificateTask(t, a, "cancelled", "failed")
	reports, err := a.Tasks.ListTaskDeploymentReports(t.Context(), task.ID)
	if err != nil || len(reports) != 1 {
		t.Fatalf("等待取消丢失上次尝试报告：%+v %v", reports, err)
	}
}

func TestCertificateCompletionFailureRollsBackAllRecords(t *testing.T) {
	for _, table := range []string{"task_deployment_reports", "task_deployment_results", "audit_logs", "task_attempts", "tasks"} {
		t.Run(table, func(t *testing.T) {
			w, a := newCertificateTaskFixture(t)
			calls := 0
			_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond,
				func(context.Context, string) (CertificateWorkflowResult, error) {
					calls++
					operation := "INSERT"
					if table == "tasks" || table == "task_attempts" {
						operation = "UPDATE"
					}
					// 表名只来自本测试的固定枚举，不接受用户输入。
					if _, err := a.Database.DB.ExecContext(t.Context(), "CREATE TRIGGER IF NOT EXISTS reject_completion BEFORE "+operation+" ON "+table+" BEGIN SELECT RAISE(ABORT, 'completion unavailable'); END"); err != nil {
						t.Fatal(err)
					}
					return CertificateWorkflowResult{Deployment: deployment.Report{Status: "success", Total: 1, Success: 1,
						Results: []deployment.Result{{ServerID: "target", Status: "success"}}}}, nil
				})
			if err == nil || calls != 1 {
				t.Fatalf("保存失败必须返回错误且不重复外部操作：%d %v", calls, err)
			}
			tasks, err := a.Tasks.ListTasks(t.Context(), "task-domain", 10)
			if err != nil || len(tasks) != 1 || tasks[0].Status != "running" || tasks[0].FinishedAt != nil {
				t.Fatalf("事务失败必须保留活动任务供恢复：%+v %v", tasks, err)
			}
			attempts, err := a.Tasks.ListAttempts(t.Context(), tasks[0].ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != "running" || attempts[0].FinishedAt != nil {
				t.Fatalf("尝试终态未回滚：%+v %v", attempts, err)
			}
			for _, stored := range []string{"task_deployment_reports", "task_deployment_results", "audit_logs"} {
				var count int
				query := "SELECT COUNT(*) FROM " + stored + " WHERE task_id = ?"
				if stored == "audit_logs" {
					query += " AND status <> 'running'"
				}
				if err := a.Database.DB.QueryRowContext(t.Context(), query, tasks[0].ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("失败后仍残留部分记录：%s %d %v", stored, count, err)
				}
			}
		})
	}
}
