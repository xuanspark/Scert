package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func newCertificateTaskFixture(t *testing.T) (*certificateTaskWorkflow, *App) {
	t.Helper()
	a, err := New(t.Context(), readyApplicationConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	item, err := domain.NewDomain("task.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.ID = "task-domain"
	if err := a.Repository.Create(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	return &certificateTaskWorkflow{
		tasks: a.Tasks, audit: repository.NewSQLAuditRepository(a.Database.DB),
	}, a
}

func assertCertificateTask(t *testing.T, a *App, status string, statuses ...string) repository.Task {
	t.Helper()
	items, err := a.Tasks.ListTasks(t.Context(), "task-domain", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("任务数量错误：%+v %v", items, err)
	}
	item := items[0]
	if item.ScopeType != repository.ScopeProject || item.ProjectID != domain.DefaultProjectID || item.OperatorID != "" {
		t.Fatalf("后台任务范围错误或伪造操作者：%+v", item)
	}
	if item.Status != status || item.StartedAt == nil || item.FinishedAt == nil {
		t.Fatalf("任务未正确收尾：%+v", item)
	}
	attempts, err := a.Tasks.ListAttempts(t.Context(), item.ID)
	if err != nil || len(attempts) != len(statuses) {
		t.Fatalf("尝试数量错误：%+v %v", attempts, err)
	}
	for i, attempt := range attempts {
		if attempt.AttemptNumber != i+1 || attempt.Status != statuses[i] || attempt.FinishedAt == nil {
			t.Fatalf("尝试结果错误：%+v", attempt)
		}
		if strings.Contains(attempt.Message, "secret-path") {
			t.Fatal("尝试记录泄露内部错误")
		}
	}
	var count int
	if err := a.Database.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs WHERE action = ? AND resource_id = ? AND status = ?", item.TaskType, item.DomainID, status).Scan(&count); err != nil || count != 1 {
		t.Fatalf("审计记录缺失：%d %v", count, err)
	}
	if err := a.Database.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs WHERE task_id = ? AND scope_type = 'project' AND project_id = ? AND operator_id = '' AND status IN ('running', ?)", item.ID, item.ProjectID, status).Scan(&count); err != nil || count != 2 {
		t.Fatalf("后台任务开始与收尾审计关联缺失：%d，%v", count, err)
	}
	return item
}

func TestCertificateTasksRecordSuccessFailureAndCancellation(t *testing.T) {
	for _, status := range []string{"success", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			w, a := newCertificateTaskFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := w.run(ctx, "task-domain", "renewal_run", "certificate_issue", 1, 0, func(context.Context, string) (CertificateWorkflowResult, error) {
				if status == "cancelled" {
					cancel()
					return CertificateWorkflowResult{}, context.Canceled
				}
				if status == "failed" {
					return CertificateWorkflowResult{}, errors.New("secret-path")
				}
				return CertificateWorkflowResult{}, nil
			})
			if (err != nil) != (status != "success") {
				t.Fatalf("执行结果错误：%v", err)
			}
			assertCertificateTask(t, a, status, status)
		})
	}
}

func TestCertificateTasksRecordOperatorFromWorkflowContext(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	ctx := repository.WithTaskOperator(t.Context(), "operator-1")
	_, err := w.run(ctx, "task-domain", "certificate_validate", "certificate_validate", 1, 0, func(context.Context, string) (CertificateWorkflowResult, error) {
		return CertificateWorkflowResult{}, nil
	})
	if err != nil {
		t.Fatalf("带操作者的证书任务执行失败：%v", err)
	}
	items, err := a.Tasks.ListTasks(t.Context(), "task-domain", 10)
	if err != nil || len(items) != 1 || items[0].OperatorID != "operator-1" {
		t.Fatalf("任务创建人记录错误：%+v %v", items, err)
	}
	var operators []string
	rows, err := a.Database.DB.QueryContext(t.Context(), "SELECT operator_id FROM audit_logs WHERE task_id = ? ORDER BY created_at, id", items[0].ID)
	if err != nil {
		t.Fatalf("读取任务审计失败：%v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var operator string
		if err := rows.Scan(&operator); err != nil {
			t.Fatalf("读取任务审计操作者失败：%v", err)
		}
		operators = append(operators, operator)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历任务审计失败：%v", err)
	}
	if len(operators) != 2 || operators[0] != "operator-1" || operators[1] != "operator-1" {
		t.Fatalf("任务审计创建人记录错误：%v", operators)
	}
}

func TestCertificateTasksKeepRetriesInOneTask(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	calls := 0
	_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond, func(context.Context, string) (CertificateWorkflowResult, error) {
		calls++
		if calls < 3 {
			return CertificateWorkflowResult{}, errors.New("secret-path")
		}
		return CertificateWorkflowResult{}, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("重试次数错误：%d %v", calls, err)
	}
	assertCertificateTask(t, a, "success", "failed", "failed", "success")
}

func TestCertificateTasksDoNotReissueAfterDeploymentFailure(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	calls := 0
	_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond, func(context.Context, string) (CertificateWorkflowResult, error) {
		calls++
		return CertificateWorkflowResult{Certificate: certificate.IssueResult{Saved: certificate.SavedResource{CertificatePath: "cert.pem"}}}, errors.New("secret-path")
	})
	if err == nil || calls != 1 {
		t.Fatalf("部署失败不应重复签发：%d %v", calls, err)
	}
	assertCertificateTask(t, a, "failed", "failed")
}

type failingCertificateTasks struct {
	repository.TaskRepository
	failCreate bool
	failFinish bool
}

func (r failingCertificateTasks) CreateTask(ctx context.Context, task repository.Task) error {
	if r.failCreate {
		return errors.New("task-store-unavailable")
	}
	return r.TaskRepository.CreateTask(ctx, task)
}

func (r failingCertificateTasks) FinishAttempt(ctx context.Context, id, status, message string, at *time.Time) error {
	if r.failFinish {
		return errors.New("attempt-store-unavailable")
	}
	return r.TaskRepository.FinishAttempt(ctx, id, status, message, at)
}

func TestCertificateTaskPersistenceFailuresStopExecutionOrRetries(t *testing.T) {
	for _, failCreate := range []bool{true, false} {
		w, a := newCertificateTaskFixture(t)
		w.tasks = failingCertificateTasks{TaskRepository: a.Tasks, failCreate: failCreate, failFinish: !failCreate}
		calls := 0
		_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond, func(context.Context, string) (CertificateWorkflowResult, error) {
			calls++
			return CertificateWorkflowResult{}, errors.New("secret-path")
		})
		if err == nil || (failCreate && calls != 0) || (!failCreate && calls != 1) {
			t.Fatalf("记录失败未阻止副作用或重复执行：%d %v", calls, err)
		}
	}
}

func TestCertificateTaskEntryPointsRecordRenewalAndDeployment(t *testing.T) {
	for _, renew := range []bool{true, false} {
		w, a := newCertificateTaskFixture(t)
		service := &workflowCertificateService{result: certificate.IssueResult{
			DomainID: "task-domain", Saved: certificate.SavedResource{CertificatePath: "cert.pem"},
		}}
		w.workflow = &CertificateWorkflow{
			Certificates: service, Domains: &workflowDomainState{},
			Servers:            workflowServerLister{servers: []deployment.Server{{ID: "server"}}},
			Deployment:         &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
			CurrentCertificate: func(context.Context, string) (certificate.IssueResult, error) { return service.result, nil },
		}
		var err error
		if renew {
			err = w.Renew(t.Context(), "task-domain", false, certificate.RenewalPolicy{})
		} else {
			_, err = w.DeployCurrent(t.Context(), "task-domain")
		}
		if err != nil || service.called != renew {
			t.Fatalf("任务入口执行错误：%v", err)
		}
		item := assertCertificateTask(t, a, "success", "success")
		if (renew && item.TaskType != "renewal_run") || (!renew && item.TaskType != "certificate_deploy") {
			t.Fatalf("任务类型错误：%+v", item)
		}
	}
}

type failingCertificateAudit struct {
	repository.AuditRepository
	failStatus string
	failure    error
}

func (r failingCertificateAudit) CreateAuditLog(ctx context.Context, item repository.AuditLog) error {
	if item.Status == r.failStatus {
		return r.failure
	}
	return r.AuditRepository.CreateAuditLog(ctx, item)
}

type failingCertificateTerminal struct {
	repository.TaskRepository
	failure error
}

func (r failingCertificateTerminal) UpdateTask(ctx context.Context, id, status, stage, message string, started, finished *time.Time) error {
	if finished != nil {
		return r.failure
	}
	return r.TaskRepository.UpdateTask(ctx, id, status, stage, message, started, finished)
}

func TestCertificateAuditFailuresPersistFailedTaskWithoutReissue(t *testing.T) {
	for _, failStatus := range []string{"running", "success"} {
		t.Run(failStatus, func(t *testing.T) {
			w, a := newCertificateTaskFixture(t)
			failure := errors.New("audit-store-unavailable")
			w.audit = failingCertificateAudit{AuditRepository: w.audit, failStatus: failStatus, failure: failure}
			calls := 0
			var logged error
			w.logError = func(err error) { logged = err }
			_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond, func(context.Context, string) (CertificateWorkflowResult, error) {
				calls++
				return CertificateWorkflowResult{}, nil
			})
			expectedCalls := 1
			if failStatus == "running" {
				expectedCalls = 0
			}
			if !errors.Is(err, failure) || !errors.Is(logged, failure) || calls != expectedCalls {
				t.Fatalf("calls=%d error=%v logged=%v", calls, err, logged)
			}
			tasks, listErr := a.Tasks.ListTasks(t.Context(), "task-domain", 10)
			if listErr != nil || len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].FinishedAt == nil {
				t.Fatalf("tasks=%+v error=%v", tasks, listErr)
			}
			if failStatus == "success" && tasks[0].Stage != "record" {
				t.Fatalf("stage=%s", tasks[0].Stage)
			}
			if strings.Contains(tasks[0].Message, failure.Error()) {
				t.Fatal("audit-store-unavailable")
			}
		})
	}
}

func TestCertificateTerminalPersistenceFailureIsReturnedWithoutReissue(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	failure := errors.New("task-terminal-unavailable")
	w.tasks = failingCertificateTerminal{TaskRepository: a.Tasks, failure: failure}
	calls := 0
	var logged error
	w.logError = func(err error) { logged = err }
	_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond, func(context.Context, string) (CertificateWorkflowResult, error) {
		calls++
		return CertificateWorkflowResult{}, nil
	})
	if calls != 1 || !errors.Is(err, failure) || !errors.Is(logged, failure) {
		t.Fatalf("calls=%d error=%v logged=%v", calls, err, logged)
	}
}

func TestCertificateTasksBindCancellationHook(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	registerCh := make(chan context.CancelFunc, 1)
	unregisterCh := make(chan string, 1)
	ctx := repository.WithTaskCancellationHooks(t.Context(), repository.TaskCancellationHooks{
		Register:   func(_ string, cancel context.CancelFunc) { registerCh <- cancel },
		Unregister: func(taskID string) { unregisterCh <- taskID },
	})
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := w.run(ctx, "task-domain", "certificate_validate", "certificate_validate", 1, 0, func(ctx context.Context, _ string) (CertificateWorkflowResult, error) {
			close(started)
			<-ctx.Done()
			return CertificateWorkflowResult{}, ctx.Err()
		})
		finished <- err
	}()
	<-started
	cancel := <-registerCh
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("task cancellation error = %v", err)
	}
	select {
	case <-unregisterCh:
	case <-time.After(time.Second):
		t.Fatal("task cancellation hook was not unregistered")
	}
	assertCertificateTask(t, a, "cancelled", "cancelled")
}

func TestCertificateTasksRecordPartialDeploymentFailure(t *testing.T) {
	w, a := newCertificateTaskFixture(t)
	_, err := w.run(t.Context(), "task-domain", "renewal_run", "certificate_issue", 1, 0, func(context.Context, string) (CertificateWorkflowResult, error) {
		return CertificateWorkflowResult{
			Deployment: deployment.Report{Total: 2, Success: 1, Failed: 1, Status: "partial"},
		}, errors.New("partial deployment")
	})
	if err == nil {
		t.Fatal("partial deployment error should remain visible to the caller")
	}
	assertCertificateTask(t, a, "partial_success", "partial_success")
}
