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
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAsyncRenewalLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "partial", "failure", "nested_error", "panic", "cancel", "shutdown", "timeout", "replay", "duplicate", "revoked", "demoted", "full", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
			defer cleanup()
			var calls atomic.Int32
			server.runRenewal = func(ctx context.Context) (certificate.RenewalReport, error) {
				calls.Add(1)
				if ctx.Err() != nil || repository.TaskOperatorFromContext(ctx) != "test-super-admin" {
					t.Error("后台续期上下文错误或继承已断开的请求")
				}
				report := certificate.RenewalReport{Checked: 3, NearExpiry: 2, Selected: 2, Urgent: 1, Succeeded: 2}
				switch scenario {
				case "partial":
					report.Succeeded, report.Failed = 1, 1
				case "failure":
					return certificate.RenewalReport{}, errors.New("private-renewal-secret")
				case "nested_error":
					return certificate.RenewalReport{Failed: 1, Errors: []string{"private-renewal-secret"}}, nil
				case "panic":
					panic("private-renewal-secret")
				case "timeout":
					return report, context.DeadlineExceeded
				}
				return report, nil
			}
			server.asyncTasks = make(map[string]bool)
			server.taskSlots = make(chan struct{}, 1)
			server.taskSlots <- struct{}{}
			if scenario == "full" {
				for index := range 32 {
					server.asyncTasks[string(rune('a'+index))] = true
				}
			}
			if scenario == "stopped" {
				server.stopTaskWorkers()
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil)
			request.Header.Set("Idempotency-Key", "renewal-check-test")
			ctx, cancel := context.WithCancel(request.Context())
			response := httptest.NewRecorder()
			serveSuperAdminHTTP(server, response, request.WithContext(ctx))
			cancel()
			if scenario == "full" || scenario == "stopped" {
				if response.Code != http.StatusServiceUnavailable {
					t.Fatalf("未拒绝不可执行任务：%d", response.Code)
				}
				items, err := tasks.ListTasks(t.Context(), repository.RenewalCheckResourceID, 10)
				if err != nil || len(items) != 1 || items[0].Status != "failed" || calls.Load() != 0 {
					t.Fatalf("拒绝后遗留任务：%+v %v", items, err)
				}
				return
			}
			task := submittedTask(t, response)
			if calls.Load() != 0 || task.ScopeType != repository.ScopeSystem || task.ProjectID != "" {
				t.Fatalf("受理阶段已执行或伪装项目：%+v", task)
			}
			if scenario == "replay" || scenario == "duplicate" {
				repeat := httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil)
				key := "renewal-check-test"
				if scenario == "duplicate" {
					key = "renewal-check-other"
				}
				repeat.Header.Set("Idempotency-Key", key)
				response = httptest.NewRecorder()
				serveSuperAdminHTTP(server, response, repeat)
				if (response.Code != http.StatusOK && response.Code != http.StatusConflict) || !strings.Contains(response.Body.String(), task.ID) || !strings.Contains(response.Body.String(), `"replayed":true`) {
					t.Fatalf("未找回原任务：%s", response.Body.String())
				}
			}
			want, wantCalls := "success", int32(1)
			switch scenario {
			case "partial":
				want = "partial_success"
			case "failure", "nested_error", "panic", "timeout":
				want = "failed"
			case "cancel":
				response = httptest.NewRecorder()
				serveSuperAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
				if response.Code != http.StatusAccepted {
					t.Fatalf("无法取消全局任务：%d %s", response.Code, response.Body.String())
				}
				want, wantCalls = "cancelled", 0
			case "shutdown":
				server.stopTaskWorkers()
				want, wantCalls = "cancelled", 0
			case "revoked", "demoted":
				user, err := server.users.GetUserByID(t.Context(), task.OperatorID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "revoked" {
					user.Status = repository.UserStatusDisabled
				} else {
					user.Role = repository.UserRoleAdmin
				}
				if err := server.users.UpdateUser(t.Context(), user); err != nil {
					t.Fatal(err)
				}
				want, wantCalls = "failed", 0
			}
			<-server.taskSlots
			waitTaskWorkersForTest(t, server)
			stored, err := tasks.GetTask(t.Context(), task.ID)
			if err != nil || stored.Status != want || calls.Load() != wantCalls || strings.Contains(stored.Message, "private-renewal-secret") {
				t.Fatalf("续期终态错误：%+v %v calls=%d", stored, err, calls.Load())
			}
			attempts, err := tasks.ListAttempts(t.Context(), task.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != want || attempts[0].Message != stored.Message || attempts[0].ScopeType != repository.ScopeSystem {
				t.Fatalf("尝试与任务不一致：%+v %v", attempts, err)
			}
			for _, role := range []string{repository.UserRoleAdmin, repository.UserRoleUser} {
				response = httptest.NewRecorder()
				request = httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID, nil)
				request.AddCookie(httpUserSessionCookie(server, "renewal-viewer-"+role, role))
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusForbidden {
					t.Fatalf("普通账户可读取全局统计：%s %d", role, response.Code)
				}
			}
		})
	}
}

func TestAsyncRenewalRunningCancellationPreservesCounts(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	started := make(chan struct{})
	server.runRenewal = func(ctx context.Context) (certificate.RenewalReport, error) {
		close(started)
		<-ctx.Done()
		// 下游错误地忽略取消返回成功时，父任务仍不得宣称全部成功。
		return certificate.RenewalReport{Checked: 3, Selected: 2, Succeeded: 1}, nil
	}
	response := httptest.NewRecorder()
	serveSuperAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil))
	task := submittedTask(t, response)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("续期未开始")
	}
	response = httptest.NewRecorder()
	serveSuperAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("取消未接收：%d", response.Code)
	}
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "cancelled" || !strings.Contains(stored.Message, "成功 1 项") {
		t.Fatalf("取消丢失已执行事实：%+v %v", stored, err)
	}
}

type renewalTaskDomainLister struct{}

func (renewalTaskDomainLister) List(context.Context) ([]domain.Domain, error) {
	return []domain.Domain{{ID: "domain-1", Name: "example.test", AutoRenew: true}}, nil
}

type renewalTaskIssuer struct{ calls atomic.Int32 }

func (i *renewalTaskIssuer) Issue(context.Context, string) (certificate.IssueResult, error) {
	i.calls.Add(1)
	return certificate.IssueResult{}, nil
}

type renewalTaskManagement struct{ calls atomic.Int32 }

func (m *renewalTaskManagement) RunRenewal(context.Context, certificate.RenewalPolicy, time.Time) (certificate.RenewalReport, error) {
	m.calls.Add(1)
	return certificate.RenewalReport{Checked: 1, Selected: 1, Succeeded: 1}, nil
}

func TestAsyncRenewalPreservesManagementAndDomainScheduler(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	issuer := &renewalTaskIssuer{}
	management := &renewalTaskManagement{}
	scheduler := certificate.NewRenewalSchedulerWithManagement(renewalTaskDomainLister{}, issuer, certificate.RenewalPolicy{}, management)
	server.runRenewal = scheduler.RunOnce
	response := httptest.NewRecorder()
	serveSuperAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "success" || issuer.calls.Load() != 1 || management.calls.Load() != 1 || !strings.Contains(stored.Message, "检查 2 项") || !strings.Contains(stored.Message, "成功 2 项") {
		t.Fatalf("真实调度器未完整运行管理及业务续期：%+v %v business=%d management=%d", stored, err, issuer.calls.Load(), management.calls.Load())
	}
}
