package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

type failingTaskStartRepository struct {
	repository.TaskRepository
}

func (r *failingTaskStartRepository) UpdateTask(ctx context.Context, id, status, stage, message string, startedAt, finishedAt *time.Time) error {
	if status == "running" {
		return errors.New("模拟任务启动状态写入失败")
	}
	return r.TaskRepository.UpdateTask(ctx, id, status, stage, message, startedAt, finishedAt)
}

func TestTaskAPIClosesTaskWhenStartStateUpdateFails(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		t.Fatal("任务启动失败时不应执行证书申请")
		return certificate.IssueResult{}, deployment.Report{}, nil
	})
	defer closeDB()
	server.tasks = &failingTaskStartRepository{TaskRepository: taskRepo}

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "更新任务状态失败") {
		t.Fatalf("任务启动状态写入失败响应错误：%d %s", response.Code, response.Body.String())
	}
	items, err := taskRepo.ListTasks(context.Background(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" || items[0].Message != "任务启动失败" {
		t.Fatalf("任务启动失败后未正确收敛任务状态：%+v %v", items, err)
	}
	attempts, err := taskRepo.ListAttempts(context.Background(), items[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" || attempts[0].Message != "任务启动失败" {
		t.Fatalf("任务启动失败后未正确收敛尝试状态：%+v %v", attempts, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 1 || logs[0].Status != "error" || logs[0].Message != "任务启动失败" {
		t.Fatalf("任务启动失败后审计记录错误：%+v %v", logs, err)
	}
}

func TestTaskAPIReturnsFailureAndKeepsTaskTrace(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{}, deployment.Report{
			Status:  "failed",
			Results: []deployment.Result{{Status: "failed", Message: "上传远程证书失败：/etc/nginx/private/key.pem: permission denied"}},
		}, errors.New("模拟证书申请失败：/etc/scert/secret/config.json")
	})
	defer closeDB()

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "证书申请或部署失败") {
		t.Fatalf("失败任务响应错误：%d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/etc/scert/secret/config.json") || strings.Contains(response.Body.String(), "/etc/nginx/private/key.pem") || strings.Contains(response.Body.String(), "permission denied") {
		t.Fatalf("失败任务响应泄露底层错误：%s", response.Body.String())
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
	if payload.Task.Status != "failed" || payload.Task.Stage != "certificate_issue" || payload.Task.Message != "证书申请或部署失败" {
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

func TestTaskAPICannotCancelManagementTaskWithoutExecutionControl(t *testing.T) {
	server, tasks, _, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	item := repository.Task{ID: "management-task", DomainID: "management-certificate", TaskType: "management_renewal", Status: "running", Stage: "activate"}
	if err := tasks.CreateTask(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+item.ID, nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("不应仅修改管理任务取消状态：%d %s", response.Code, response.Body.String())
	}
	stored, err := tasks.GetTask(t.Context(), item.ID)
	if err != nil || stored.Status != "running" {
		t.Fatalf("拒绝取消后任务状态变化：%+v %v", stored, err)
	}
}

func TestTaskAPICanCancelRunningIssue(t *testing.T) {
	started := make(chan struct{})
	server, taskRepo, _, closeDB := newTaskAPITestServer(t, func(ctx context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		close(started)
		<-ctx.Done()
		return certificate.IssueResult{DomainID: id}, deployment.Report{}, ctx.Err()
	})
	defer closeDB()

	issueResponse := httptest.NewRecorder()
	issueDone := make(chan struct{})
	go func() {
		serveAdminHTTP(server, issueResponse, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
		close(issueDone)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("证书任务没有进入执行状态")
	}
	items, err := taskRepo.ListTasks(context.Background(), "", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("读取运行中任务失败：%+v %v", items, err)
	}
	cancelResponse := httptest.NewRecorder()
	serveAdminHTTP(server, cancelResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+items[0].ID, nil))
	if cancelResponse.Code != http.StatusOK || !strings.Contains(cancelResponse.Body.String(), "cancelled") {
		t.Fatalf("取消任务响应错误：%d %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	select {
	case <-issueDone:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后证书任务没有结束")
	}
	if issueResponse.Code != http.StatusConflict {
		t.Fatalf("取消任务的申请请求应返回 409：%d %s", issueResponse.Code, issueResponse.Body.String())
	}
	item, err := taskRepo.GetTask(context.Background(), items[0].ID)
	if err != nil || item.Status != "cancelled" {
		t.Fatalf("任务取消状态没有持久化：%+v %v", item, err)
	}
}

func TestTaskAPIReplaysIdempotentIssue(t *testing.T) {
	calls := 0
	server, taskRepo, _, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		calls++
		return certificate.IssueResult{DomainID: id}, deployment.Report{Status: "success"}, nil
	})
	defer closeDB()

	firstRequest := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil)
	firstRequest.Header.Set("Idempotency-Key", "issue-key-20260830")
	firstResponse := httptest.NewRecorder()
	serveAdminHTTP(server, firstResponse, firstRequest)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("首次幂等请求失败：%d %s", firstResponse.Code, firstResponse.Body.String())
	}
	secondRequest := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil)
	secondRequest.Header.Set("Idempotency-Key", "issue-key-20260830")
	secondResponse := httptest.NewRecorder()
	serveAdminHTTP(server, secondResponse, secondRequest)
	if secondResponse.Code != http.StatusOK || !strings.Contains(secondResponse.Body.String(), "replayed") {
		t.Fatalf("重复幂等请求响应错误：%d %s", secondResponse.Code, secondResponse.Body.String())
	}
	if calls != 1 {
		t.Fatalf("幂等请求重复执行了证书申请：%d", calls)
	}
	items, err := taskRepo.ListTasks(context.Background(), "", 10)
	if err != nil || len(items) != 1 || items[0].IdempotencyKey != "issue-key-20260830" {
		t.Fatalf("幂等键没有正确保存：%+v %v", items, err)
	}
}

func TestTaskAPIReturnsNotImplementedWhenTaskDependenciesAreMissing(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	server := New(Dependencies{
		Config: cfg,
		Tasks:  (*repository.SQLTaskRepository)(nil),
		IssueCertificate: func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{}, nil
		},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "证书任务接口尚未配置") {
		t.Fatalf("任务依赖缺失时应返回 501：%d %s", response.Code, response.Body.String())
	}
}

func TestIssueDomainSanitizesDeploymentReportWithoutTaskRepository(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	server := New(Dependencies{
		Config:     cfg,
		Repository: &stubDomainRepository{},
		IssueCertificate: func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{DomainID: "domain-1"}, deployment.Report{
				Status: "failed",
				Results: []deployment.Result{{
					ServerID: "server-1",
					Server:   "生产服务器",
					Status:   "failed",
					Message:  "替换远程证书失败：/etc/scert/private/key.pem: permission denied",
				}},
			}, nil
		},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("无任务仓储时证书申请响应错误：%d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/etc/scert/private/key.pem") || strings.Contains(response.Body.String(), "permission denied") {
		t.Fatalf("直接证书申请响应泄露部署错误：%s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "证书部署失败") {
		t.Fatalf("直接证书申请响应未返回稳定部署错误摘要：%s", response.Body.String())
	}
}
