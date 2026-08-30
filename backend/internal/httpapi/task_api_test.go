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
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func newTaskAPITestServer(t *testing.T, issue func(context.Context, string) (certificate.IssueResult, deployment.Report, error)) (*Server, *repository.SQLTaskRepository, *repository.SQLAuditRepository, func()) {
	t.Helper()
	cfg := readyTestConfig(t.TempDir())
	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开测试数据库失败：%v", err)
	}
	if err := database.Migrate(context.Background(), db.DB, cfg.Database.Driver); err != nil {
		db.Close()
		t.Fatalf("迁移测试数据库失败：%v", err)
	}
	domainRepo := repository.NewSQLDomainRepository(db.DB)
	taskRepo := repository.NewSQLTaskRepository(db.DB)
	auditRepo := repository.NewSQLAuditRepository(db.DB)
	item, err := domain.NewDomain("example.com", domain.CertificateSingle, nil)
	item.ID = "domain-1"
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := domainRepo.Create(context.Background(), item); err != nil {
		db.Close()
		t.Fatalf("创建测试域名失败：%v", err)
	}
	server := New(Dependencies{
		Config:           cfg,
		Repository:       domainRepo,
		IssueCertificate: issue,
		Tasks:            taskRepo,
		Audit:            auditRepo,
	})
	return server, taskRepo, auditRepo, func() { _ = db.Close() }
}

func TestTaskAPIRecordsSuccessfulIssueAndAudit(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{DomainID: id}, deployment.Report{Status: "success"}, nil
	})
	defer closeDB()

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("成功任务响应错误：%d %s", response.Code, response.Body.String())
	}
	items, err := taskRepo.ListTasks(context.Background(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "success" {
		t.Fatalf("任务没有进入成功状态：%+v %v", items, err)
	}
	attempts, err := taskRepo.ListAttempts(context.Background(), items[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "success" {
		t.Fatalf("任务尝试记录错误：%+v %v", attempts, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 1 || logs[0].Status != "success" {
		t.Fatalf("审计日志记录错误：%+v %v", logs, err)
	}

	list := httptest.NewRecorder()
	serveAdminHTTP(server, list, httptest.NewRequest(http.MethodGet, "/api/v1/tasks?limit=10", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), items[0].ID) {
		t.Fatalf("任务列表响应错误：%d %s", list.Code, list.Body.String())
	}

	detail := httptest.NewRecorder()
	serveAdminHTTP(server, detail, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+items[0].ID, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "success") {
		t.Fatalf("任务详情响应错误：%d %s", detail.Code, detail.Body.String())
	}

	attemptList := httptest.NewRecorder()
	serveAdminHTTP(server, attemptList, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+items[0].ID+"/attempts", nil))
	if attemptList.Code != http.StatusOK || !strings.Contains(attemptList.Body.String(), "certificate_issue") {
		t.Fatalf("任务尝试接口响应错误：%d %s", attemptList.Code, attemptList.Body.String())
	}

	logList := httptest.NewRecorder()
	serveAdminHTTP(server, logList, httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?limit=10", nil))
	if logList.Code != http.StatusOK || !strings.Contains(logList.Body.String(), "certificate_issue") {
		t.Fatalf("审计日志接口响应错误：%d %s", logList.Code, logList.Body.String())
	}
}

func TestTaskAPIReturnsFailureAndKeepsTaskTrace(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{}, deployment.Report{Status: "failed"}, errors.New("模拟证书申请失败")
	})
	defer closeDB()

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "模拟证书申请失败") {
		t.Fatalf("失败任务响应错误：%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Task struct {
			Status     string `json:"status"`
			Stage      string `json:"stage"`
			Message    string `json:"message"`
			FinishedAt string `json:"finished_at"`
			UpdatedAt  string `json:"updated_at"`
		} `json:"task"`
		TaskStatus string `json:"task_status"`
		FinishedAt string `json:"finished_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("失败任务响应不是有效 JSON：%v；响应：%s", err, response.Body.String())
	}
	if payload.Task.Status != "failed" || payload.Task.Stage != "certificate_issue" || payload.Task.Message != "模拟证书申请失败" {
		t.Fatalf("失败任务嵌套状态不一致：%+v", payload.Task)
	}
	if payload.TaskStatus != "failed" || payload.FinishedAt == "" || payload.Task.FinishedAt == "" || payload.Task.UpdatedAt == "" {
		t.Fatalf("失败任务顶层状态或时间字段不完整：%+v", payload)
	}
	items, err := taskRepo.ListTasks(context.Background(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("失败任务状态错误：%+v %v", items, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 1 || logs[0].Status != "error" {
		t.Fatalf("失败审计状态错误：%+v %v", logs, err)
	}
}

func TestTaskAPIRejectsUnknownTask(t *testing.T) {
	server, _, _, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/unknown", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("未知任务应返回 404：%d %s", response.Code, response.Body.String())
	}
}
