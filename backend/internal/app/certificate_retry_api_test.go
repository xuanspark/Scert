package app

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

func TestApplicationDeployRetryUsesExistingTask(t *testing.T) {
	_, application := newCertificateTaskFixture(t)
	saved := workflowSavedCertificate(t)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	transactions := make(chan deployment.TransactionContext, 2)
	application.Workflow.CurrentCertificate = func(ctx context.Context, id string) (certificate.IssueResult, error) {
		transaction, _ := deployment.TransactionContextFromContext(ctx)
		transactions <- transaction
		if calls.Add(1) == 1 {
			return certificate.IssueResult{}, errors.New("首次部署故障")
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return certificate.IssueResult{}, ctx.Err()
		}
		return certificate.IssueResult{DomainID: id, Domains: []string{"example.com"}, Saved: saved}, nil
	}
	application.Workflow.Servers = workflowServerLister{servers: []deployment.Server{{ID: "server-1"}, {ID: "server-2"}}}
	application.Workflow.Deployment = &workflowDeployer{report: deployment.Report{
		Total: 2, Success: 2, Status: "success",
		Results: []deployment.Result{{ServerID: "server-1", Status: "success"}, {ServerID: "server-2", Status: "success"}},
	}}
	application.Workflow.Notifier = nil
	application.Workflow.RemoteVerifierForServer = func(context.Context, deployment.Server, string, []string) (certificate.ValidationResult, error) {
		return certificate.ValidationResult{FingerprintSHA256: saved.FingerprintSHA256}, nil
	}
	var cookies []*http.Cookie
	request := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		application.HTTP.Handler().ServeHTTP(response, req)
		return response
	}
	login := request("/api/v1/auth/login", `{"identifier":"admin","password":"Test1234!"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("真实应用登录失败：%d %s", login.Code, login.Body.String())
	}
	cookies = login.Result().Cookies()
	initial := request("/api/v1/domains/task-domain/deploy", "")
	if initial.Code != http.StatusAccepted {
		t.Fatalf("首次部署没有异步接收：%d %s", initial.Code, initial.Body.String())
	}
	waitTerminal := func(status string) []repository.Task {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			items, err := application.Tasks.ListTasks(t.Context(), "task-domain", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) == 1 && items[0].Status == status {
				return items
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("等待任务终态超时：%s", status)
		return nil
	}
	tasks := waitTerminal("failed")
	if calls.Load() != 1 {
		t.Fatal("首次故障未经过真实部署接线")
	}
	task := tasks[0]
	retryPath := "/api/v1/tasks/" + task.ID + "/retry"
	retry := request(retryPath, "")
	// 终态持久化后执行器可能仍在释放资源，收尾期间重试必须保持拒绝。
	deadline := time.Now().Add(5 * time.Second)
	for retry.Code == http.StatusConflict && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		retry = request(retryPath, "")
	}
	if retry.Code != http.StatusAccepted {
		t.Fatalf("真实部署重试未接收：%d %s", retry.Code, retry.Body.String())
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("重试工作流启动超时")
	}
	concurrent := request(retryPath, "")
	if concurrent.Code != http.StatusConflict || calls.Load() != 2 {
		t.Fatalf("并发重试未被阻止：%d %s", concurrent.Code, concurrent.Body.String())
	}
	release <- struct{}{}
	tasks = waitTerminal("success")
	if tasks[0].ID != task.ID {
		t.Fatalf("重试重复创建任务：%+v", tasks)
	}
	attempts, err := application.Tasks.ListAttempts(t.Context(), task.ID)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("尝试数量错误：%+v %v", attempts, err)
	}
	for i, status := range []string{"failed", "success"} {
		attempt := attempts[i]
		transaction := <-transactions
		if attempt.Status != status || attempt.AttemptNumber != i+1 || attempt.FinishedAt == nil ||
			transaction.TaskID != task.ID || transaction.AttemptID != attempt.ID || transaction.AttemptNumber != i+1 {
			t.Fatalf("重试丢失原任务事务关联：%+v %+v", attempt, transaction)
		}
	}
	repeated := request(retryPath, "")
	results, err := application.Tasks.ListTaskDeploymentResults(t.Context(), task.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("双服务器结果未保存：%+v %v", results, err)
	}
	for _, result := range results {
		if result.AttemptID != attempts[1].ID || result.AttemptNumber != 2 || result.Status != "success" {
			t.Fatalf("服务器结果未关联重试尝试：%+v", result)
		}
	}
	var auditCount int
	err = application.Database.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_logs WHERE task_id = ? AND action = ? AND operator_id = ? AND status IN ('running', 'success')",
		task.ID, "certificate_deploy_retry", task.OperatorID).Scan(&auditCount)
	if err != nil || auditCount != 2 {
		t.Fatalf("重试审计与操作者关联不完整：%d %v", auditCount, err)
	}
	if repeated.Code != http.StatusConflict || calls.Load() != 2 {
		t.Fatalf("成功任务仍被重复执行：%d %s", repeated.Code, repeated.Body.String())
	}
}
