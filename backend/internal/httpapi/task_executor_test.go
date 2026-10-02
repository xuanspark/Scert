package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAsyncQueueOverloadClosesPersistedTask(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		t.Error("队列已满时不能执行外部操作")
		return certificate.IssueResult{}, deployment.Report{}, nil
	})
	defer cleanup()
	server.asyncTasks = make(map[string]bool)
	for i := 0; i < 32; i++ {
		server.asyncTasks[strconv.Itoa(i)] = true
	}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("队列过载未拒绝：%d %s", response.Code, response.Body.String())
	}
	items, err := tasks.ListTasks(t.Context(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("过载后遗留活动任务：%+v %v", items, err)
	}
	attempts, err := tasks.ListAttempts(t.Context(), items[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" {
		t.Fatalf("过载后尝试未关闭：%+v %v", attempts, err)
	}
}

func TestAsyncQueuedCancellationNeverStartsOperation(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		t.Error("排队期间取消的任务不能执行外部操作")
		return certificate.IssueResult{}, deployment.Report{}, nil
	})
	defer cleanup()
	server.asyncTasks = make(map[string]bool)
	server.taskSlots = make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		server.taskSlots <- struct{}{}
	}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	task := submittedTask(t, response)
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("排队取消未接收：%d %s", response.Code, response.Body.String())
	}
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "cancelled" {
		t.Fatalf("排队取消终态错误：%+v %v", stored, err)
	}
}

func TestAsyncPanicClosesTaskWithoutExposingPanic(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		panic("private-panic-secret")
	})
	defer cleanup()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "failed" || strings.Contains(stored.Message, "private-panic-secret") {
		t.Fatalf("异常任务未安全关闭：%+v %v", stored, err)
	}
	attempts, err := tasks.ListAttempts(t.Context(), task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" || strings.Contains(attempts[0].Message, "private-panic-secret") {
		t.Fatalf("异常尝试未安全关闭：%+v %v", attempts, err)
	}
}

func TestAsyncDeadlineBeforeExecutionIsFailureNotUserCancellation(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	task := repository.Task{ID: "deadline-task", DomainID: "domain-1", TaskType: "certificate_issue", Status: "queued", Stage: "queued"}
	attempt := repository.TaskAttempt{ID: "deadline-attempt", TaskID: task.ID, AttemptNumber: 1, Status: "queued", Stage: "queued"}
	if err := tasks.CreateTask(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateAttempt(t.Context(), attempt); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	server.runCertificateTask(ctx, task, attempt, "certificate_issue", func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		t.Error("超时任务不能执行外部操作")
		return certificate.IssueResult{}, deployment.Report{}, nil
	}, "issue", "执行中", "执行完成")
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "failed" || !strings.Contains(stored.Message, "超时") {
		t.Fatalf("超时被误记为用户取消：%+v %v", stored, err)
	}
}

type failingTaskFinishAuditRepository struct{ repository.AuditRepository }

func (r failingTaskFinishAuditRepository) CreateAuditLog(ctx context.Context, item repository.AuditLog) error {
	if item.Status == "success" {
		return errors.New("模拟完成审计写入失败")
	}
	return r.AuditRepository.CreateAuditLog(ctx, item)
}

func TestAsyncFinishAuditFailureDoesNotLeaveSuccessfulAttempt(t *testing.T) {
	server, tasks, audit, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{}, deployment.Report{Status: "success"}, nil
	})
	defer cleanup()
	server.audit = failingTaskFinishAuditRepository{audit}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("完成审计失败却标记成功：%+v %v", stored, err)
	}
	attempts, err := tasks.ListAttempts(t.Context(), task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" || attempts[0].Message != stored.Message {
		t.Fatalf("任务与尝试终态不一致：%+v %v", attempts, err)
	}
}

func submittedTask(t *testing.T, response *httptest.ResponseRecorder) repository.Task {
	t.Helper()
	if response.Code != http.StatusAccepted {
		t.Fatalf("应快速返回已接收：%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Task repository.Task `json:"task"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Task.ID == "" {
		t.Fatalf("任务编号缺失：%v %s", err, response.Body.String())
	}
	return payload.Task
}

func TestAsyncTaskSurvivesRequestDisconnect(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(ctx context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		close(started)
		select {
		case <-release:
			return certificate.IssueResult{DomainID: id}, deployment.Report{Status: "success"}, nil
		case <-ctx.Done():
			return certificate.IssueResult{}, deployment.Report{}, ctx.Err()
		}
	})
	defer cleanup()
	ctx, disconnect := context.WithCancel(t.Context())
	defer disconnect()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "disconnect-request")
	done := make(chan struct{})
	go func() { serveAdminHTTP(server, response, request); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("请求仍等待工作流，未异步返回")
	}
	task := submittedTask(t, response)
	if response.Header().Get("Location") != "/api/v1/tasks/"+task.ID {
		t.Fatal("任务查询地址不正确")
	}
	disconnect()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("任务未开始")
	}
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "running" {
		t.Fatalf("断线影响任务：%+v %v", stored, err)
	}
	duplicate := httptest.NewRecorder()
	replay := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil)
	replay.Header.Set("Idempotency-Key", "disconnect-request")
	serveAdminHTTP(server, duplicate, replay)
	if duplicate.Code != http.StatusConflict {
		t.Fatal("活动任务没有拒绝重复提交")
	}
	close(release)
	waitTaskWorkersForTest(t, server)
	stored, err = tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "success" {
		t.Fatalf("后台任务未完成：%+v %v", stored, err)
	}
}

func TestAsyncCancellationBlocksRetryUntilCleanup(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(ctx context.Context, _ string) (certificate.IssueResult, deployment.Report, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return certificate.IssueResult{}, deployment.Report{}, ctx.Err()
	})
	defer cleanup()
	defer close(release)
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	task := submittedTask(t, response)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("未开始执行")
	}
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
	if response.Code != http.StatusAccepted {
		t.Fatal(response.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("取消信号未送达")
	}
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "cancelling" {
		t.Fatalf("过早发布取消终态：%+v %v", stored, err)
	}
	if err := tasks.UpdateTask(t.Context(), task.ID, "running", "deployment", "迟到进度", nil, nil); err == nil {
		t.Fatal("迟到进度覆盖取消状态")
	}
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/retry", nil))
	if response.Code != http.StatusConflict {
		t.Fatal("回滚期间允许了重试")
	}
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusConflict {
		t.Fatal("回滚期间允许了新任务")
	}
}

func TestAsyncShutdownWaitsForCleanupAndRejectsNewWork(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(ctx context.Context, _ string) (certificate.IssueResult, deployment.Report, error) {
		close(started)
		<-ctx.Done()
		<-release
		return certificate.IssueResult{}, deployment.Report{}, ctx.Err()
	})
	defer cleanup()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	task := submittedTask(t, response)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("任务未开始")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("停服没有等待收尾：%v", err)
	}
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || !isActiveTaskStatus(stored.Status) {
		close(release)
		t.Fatalf("收尾前终态错误：%+v %v", stored, err)
	}
	close(release)
	waitTaskWorkersForTest(t, server)
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("停止后仍接收任务：%d", response.Code)
	}
}

func TestAsyncDeploymentUsesExistingTaskAndRejectsCrossOperationKey(t *testing.T) {
	var calls atomic.Int32
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.deployCertificateTask = func(ctx context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		calls.Add(1)
		return certificate.IssueResult{DomainID: id}, deployment.Report{Status: "success"}, nil
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/deploy", nil)
	request.Header.Set("Idempotency-Key", "shared-operation-key")
	serveAdminHTTP(server, response, request)
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	items, err := tasks.ListTasks(t.Context(), "domain-1", 10)
	if err != nil || len(items) != 1 || items[0].ID != task.ID || items[0].Status != "success" || calls.Load() != 1 {
		t.Fatalf("部署任务重复创建或未完成：%+v %v", items, err)
	}
	server.issueCertificate = func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		calls.Add(1)
		return certificate.IssueResult{}, deployment.Report{}, nil
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil)
	request.Header.Set("Idempotency-Key", "shared-operation-key")
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusConflict || calls.Load() != 1 {
		t.Fatal("跨操作复用了幂等键")
	}
}
