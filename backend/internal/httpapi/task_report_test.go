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

func TestAsyncTaskReportRestoresVerificationAndRecovery(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{}, deployment.Report{
			Total: 1, Failed: 1, Status: "failed", VerificationStatus: "failed", RecoveryStatus: "success",
			Results: []deployment.Result{{ServerID: "private-report-target", Status: "failed", FailureCode: deployment.FailureRecoveryRestored, Message: "secret-remote-output"}},
		}, errors.New("站点校验失败，已恢复")
	})
	defer cleanup()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	path := "/api/v1/tasks/" + task.ID + "/deployment-results"
	response = httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodGet, path, nil))
	var body struct {
		Items   []repository.TaskDeploymentResult `json:"items"`
		Reports []repository.TaskDeploymentReport `json:"reports"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Items) != 1 || len(body.Reports) != 1 {
		t.Fatalf("任务完整报告接口响应错误：%d %s", response.Code, response.Body.String())
	}
	report := body.Reports[0]
	if report.TaskID != task.ID || report.AttemptNumber != 1 || report.Report.VerificationStatus != "failed" || report.Report.RecoveryStatus != "success" || report.Report.Results[0].FailureCode != deployment.FailureRecoveryRestored {
		t.Fatalf("接口遗漏验证和恢复报告：%+v", report)
	}
	if strings.Contains(response.Body.String(), "secret-remote-output") {
		t.Fatal("接口泄露原始远端输出")
	}
	outsider := httpUserSessionCookie(server, "outsider", repository.UserRoleUser)
	response = serveUserRequest(server, outsider, http.MethodGet, path, "")
	if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "private-report-target") {
		t.Fatal("完整报告未遵守项目权限")
	}
	member := httpUserSessionCookie(server, "report-reader", repository.UserRoleUser)
	if err := server.projectMembers.GrantProjectRole(t.Context(), "test-admin", domain.DefaultProjectID, "report-reader", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	response = serveUserRequest(server, member, http.MethodGet, path, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "private-report-target") {
		t.Fatal("只读成员无法读取完整报告")
	}
	if err := server.projectMembers.RevokeProjectRole(t.Context(), "test-admin", domain.DefaultProjectID, "report-reader", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	response = serveUserRequest(server, member, http.MethodGet, path, "")
	if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "private-report-target") {
		t.Fatal("撤权后仍泄露完整报告")
	}
	visible, err := tasks.ListVisibleTaskDeploymentReports(t.Context(), "report-reader", task.ID)
	if err != nil || len(visible) != 0 {
		t.Fatalf("报告仓储未实时检查权限：%+v %v", visible, err)
	}
}
