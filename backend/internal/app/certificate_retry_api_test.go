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
	if initial.Code != http.StatusBadGateway || calls.Load() != 1 {
		t.Fatalf("首次故障未经过真实部署接线：%d %s", initial.Code, initial.Body.String())
	}
	tasks, err := application.Tasks.ListTasks(t.Context(), "task-domain", 10)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "failed" {
		t.Fatalf("首次部署未生成唯一失败任务：%+v %v", tasks, err)
	}
	task := tasks[0]
	retryPath := "/api/v1/tasks/" + task.ID + "/retry"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(retryPath, "") }()
	select {
	case <-started:
	case response := <-done:
		t.Fatalf("重试未进入原始部署工作流：%d %s", response.Code, response.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("重试工作流启动超时")
	}
	concurrent := request(retryPath, "")
	if concurrent.Code != http.StatusConflict || calls.Load() != 2 {
		t.Fatalf("并发重试未被阻止：%d %s", concurrent.Code, concurrent.Body.String())
	}
	release <- struct{}{}
	select {
	case response := <-done:
		if response.Code != http.StatusOK {
			t.Fatalf("真实应用部署重试失败：%d %s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("重试工作流收尾超时")
	}
	tasks, err = application.Tasks.ListTasks(t.Context(), "task-domain", 10)
	if err != nil || len(tasks) != 1 || tasks[0].ID != task.ID || tasks[0].Status != "success" {
		t.Fatalf("重试重复创建任务或未成功：%+v %v", tasks, err)
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
