package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestTaskDeploymentResultsIssueAndRead(t *testing.T) {
	server, tasks, _, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{DomainID: id}, deployment.Report{
			Status: "partial",
			Results: []deployment.Result{
				{ServerID: "server-a", Server: "服务器甲", Status: "success", Message: "部署完成", BackupID: "backup-a"},
				{ServerID: "server-b", Server: "服务器乙", Status: "failed", Message: "/etc/scert/private/key.pem: permission denied"},
			},
		}, errors.New("部分服务器部署失败")
	})
	defer closeDB()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusMultiStatus {
		t.Fatalf("部分部署失败响应错误：%d %s", response.Code, response.Body.String())
	}
	items, err := tasks.ListTasks(t.Context(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "partial_success" {
		t.Fatalf("部分成功任务记录错误：%+v %v", items, err)
	}
	attempts, err := tasks.ListAttempts(t.Context(), items[0].ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("任务尝试记录错误：%+v %v", attempts, err)
	}
	// 模拟应用层持久化的原始错误，确保查询入口也独立脱敏。
	if err := tasks.RecordTaskDeploymentResults(t.Context(), items[0].ID, attempts[0].ID, 1, []repository.TaskDeploymentResult{
		{ResultIndex: 0, ServerID: "server-a", Server: "服务器甲", Status: "success", Message: "部署完成", BackupID: "backup-a"},
		{ResultIndex: 1, ServerID: "server-b", Server: "服务器乙", Status: "failed", Message: "/etc/scert/private/key.pem: permission denied"},
	}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+items[0].ID+"/deployment-results", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("部署结果读取失败：%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []repository.TaskDeploymentResult `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("服务器结果数量错误：%+v", payload.Items)
	}
	for _, item := range payload.Items {
		if item.TaskID != items[0].ID || item.AttemptID != attempts[0].ID || item.AttemptNumber != 1 || item.CreatedAt.IsZero() {
			t.Fatalf("部署结果任务关联缺失：%+v", item)
		}
	}
	if payload.Items[0].ServerID != "server-a" || payload.Items[0].BackupID != "backup-a" || payload.Items[0].Status != "success" {
		t.Fatalf("成功结果错误：%+v", payload.Items[0])
	}
	if payload.Items[1].ServerID != "server-b" || payload.Items[1].Status != "failed" || payload.Items[1].Message != "证书部署失败" {
		t.Fatalf("失败结果未脱敏：%+v", payload.Items[1])
	}
	for _, secret := range []string{"/etc/scert/private/key.pem", "permission denied"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("部署结果泄露底层错误：%s", response.Body.String())
		}
	}
}

func TestTaskDeploymentResultsHTTPSFailurePreservesTransferEvidence(t *testing.T) {
	server, tasks, _, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{DomainID: id}, deployment.Report{
			Total: 1, Success: 1, Status: "failed", VerificationStatus: "failed",
			Results: []deployment.Result{{ServerID: "server-a", Status: "success", Message: "文件已推送并重载"}},
		}, errors.New("HTTPS 校验失败：/secret-path 不应返回客户端")
	})
	defer closeDB()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("HTTPS 校验失败应返回失败响应：%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Deployment deployment.Report `json:"deployment"`
		TaskStatus string            `json:"task_status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TaskStatus != "failed" || payload.Deployment.Status != "failed" || payload.Deployment.VerificationStatus != "failed" {
		t.Fatalf("接口未保留整体失败与站点校验失败：%+v", payload)
	}
	if payload.Deployment.Success != 1 || payload.Deployment.Results[0].Status != "success" || strings.Contains(response.Body.String(), "/secret-path") {
		t.Fatalf("接口丢失真实推送结果或泄露底层错误：%s", response.Body.String())
	}
	items, err := tasks.ListTasks(t.Context(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("任务持久化状态错误：%+v，%v", items, err)
	}
	results, err := tasks.ListTaskDeploymentResults(t.Context(), items[0].ID)
	if err != nil || len(results) != 1 || results[0].Status != "success" {
		t.Fatalf("持久化单台推送结果错误：%+v，%v", results, err)
	}
}

func TestTaskDeploymentResultsVisibility(t *testing.T) {
	server, tasks, _, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	task := repository.Task{ID: "results-task", DomainID: "domain-1", TaskType: "certificate_issue", Status: "success", Stage: "deploy"}
	if err := tasks.CreateTask(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateAttempt(t.Context(), repository.TaskAttempt{ID: "results-attempt", TaskID: task.ID, AttemptNumber: 1, Status: "success", Stage: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.RecordTaskDeploymentResults(t.Context(), task.ID, "results-attempt", 1, []repository.TaskDeploymentResult{{ServerID: "private-server", Status: "success"}}); err != nil {
		t.Fatal(err)
	}
	adminCookie := httpUserSessionCookie(server, "test-admin", repository.UserRoleAdmin)
	outsiderCookie := httpUserSessionCookie(server, "outsider", repository.UserRoleUser)
	memberCookie := httpUserSessionCookie(server, "reader", repository.UserRoleUser)
	if err := server.projectMembers.GrantProjectRole(t.Context(), "test-admin", domain.DefaultProjectID, "reader", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		status int
	}{
		{"admin", adminCookie, http.StatusOK},
		{"member", memberCookie, http.StatusOK},
		{"outsider", outsiderCookie, http.StatusForbidden},
		{"anonymous", nil, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := serveUserRequest(server, tc.cookie, http.MethodGet, "/api/v1/tasks/"+task.ID+"/deployment-results", "")
			if response.Code != tc.status {
				t.Fatalf("部署结果权限响应错误：%d %s", response.Code, response.Body.String())
			}
			if tc.status != http.StatusOK && strings.Contains(response.Body.String(), "private-server") {
				t.Fatal("未授权响应泄露服务器信息")
			}
		})
	}
	if err := server.projectMembers.RevokeProjectRole(t.Context(), "test-admin", domain.DefaultProjectID, "reader", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	response := serveUserRequest(server, memberCookie, http.MethodGet, "/api/v1/tasks/"+task.ID+"/deployment-results", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("撤权后旧会话仍可读取部署结果：%d %s", response.Code, response.Body.String())
	}
	visible, err := tasks.ListVisibleTaskDeploymentResults(t.Context(), "reader", task.ID)
	if err != nil || len(visible) != 0 {
		t.Fatalf("仓储读取未实时隔离撤权成员：%+v %v", visible, err)
	}
}
