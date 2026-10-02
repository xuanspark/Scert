package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAsyncCancellationPreservesActualOperationOutcome(t *testing.T) {
	for _, test := range []struct {
		name   string
		report deployment.Report
		err    error
		want   string
	}{
		{name: "already_completed", report: deployment.Report{Status: "success"}, want: "success"},
		{name: "failed", report: deployment.Report{Status: "failed"}, err: errors.New("部署收尾失败"), want: "failed"},
		{name: "partially_completed", report: deployment.Report{Status: "partial"}, err: context.Canceled, want: "partial_success"},
		{name: "cancelled", err: context.Canceled, want: "cancelled"},
		{name: "recovery_unconfirmed", report: deployment.Report{Status: "failed", RecoveryStatus: "unconfirmed"}, err: errors.Join(context.Canceled, errors.New("回滚未确认")), want: "failed"},
		{name: "recovery_failed", report: deployment.Report{Status: "failed", RecoveryStatus: "failed"}, err: errors.Join(context.Canceled, errors.New("回滚失败")), want: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			server, tasks, _, cleanup := newTaskAPITestServer(t, func(ctx context.Context, _ string) (certificate.IssueResult, deployment.Report, error) {
				close(started)
				<-ctx.Done()
				return certificate.IssueResult{}, test.report, test.err
			})
			defer cleanup()
			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
			task := submittedTask(t, response)
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("任务没有开始")
			}
			response = httptest.NewRecorder()
			serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
			if response.Code != http.StatusAccepted {
				t.Fatalf("取消未被接收：%s", response.Body.String())
			}
			waitTaskWorkersForTest(t, server)
			stored, err := tasks.GetTask(t.Context(), task.ID)
			if err != nil || stored.Status != test.want {
				t.Fatalf("取消请求覆盖了实际结果：%+v %v", stored, err)
			}
			attempts, err := tasks.ListAttempts(t.Context(), task.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != stored.Status {
				t.Fatalf("任务与尝试不一致：%+v %v", attempts, err)
			}
			if test.want == "success" && !strings.Contains(stored.Message, "未撤销") {
				t.Fatal("必须提示取消未撤销已完成操作")
			}
		})
	}
}

type failingCompletionTaskRepository struct {
	repository.TaskRepository
	operationFinished *atomic.Bool
	failRead          bool
}

func (r failingCompletionTaskRepository) GetTask(ctx context.Context, id string) (repository.Task, error) {
	if r.failRead && r.operationFinished.Load() {
		return repository.Task{}, errors.New("模拟收尾状态读取失败")
	}
	return r.TaskRepository.GetTask(ctx, id)
}

func (r failingCompletionTaskRepository) FinishTaskAttempt(ctx context.Context, item repository.TaskCompletion) error {
	if !r.failRead {
		return errors.New("模拟收尾事务保存失败")
	}
	return r.TaskRepository.FinishTaskAttempt(ctx, item)
}

func TestAsyncCompletionFailureDoesNotPublishFalseSuccess(t *testing.T) {
	for _, failRead := range []bool{true, false} {
		name := "atomic_write_failure"
		if failRead {
			name = "state_read_failure"
		}
		t.Run(name, func(t *testing.T) {
			var finished atomic.Bool
			server, tasks, _, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
				finished.Store(true)
				return certificate.IssueResult{}, deployment.Report{Status: "success"}, nil
			})
			defer cleanup()
			server.tasks = failingCompletionTaskRepository{TaskRepository: tasks, operationFinished: &finished, failRead: failRead}
			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
			task := submittedTask(t, response)
			waitTaskWorkersForTest(t, server)
			stored, err := tasks.GetTask(t.Context(), task.ID)
			if err != nil || stored.Status == "success" {
				t.Fatalf("无法可靠持久化却标记成功：%+v %v", stored, err)
			}
			attempts, err := tasks.ListAttempts(t.Context(), task.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status == "success" {
				t.Fatalf("尝试被单独保存为成功：%+v %v", attempts, err)
			}
			if failRead {
				if stored.Status != "failed" || attempts[0].Status != "failed" {
					t.Fatal("状态读取失败必须闭合为失败")
				}
			} else {
				if !isActiveTaskStatus(stored.Status) || stored.FinishedAt != nil || attempts[0].FinishedAt != nil {
					t.Fatal("事务失败必须保留可由启动恢复接管的中断状态")
				}
				if count, err := tasks.RecoverInterruptedTasks(t.Context(), time.Now().UTC()); err != nil || count != 1 {
					t.Fatalf("中断记录无法恢复：%d %v", count, err)
				}
			}
		})
	}
}
