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
	if _, err := db.DB.ExecContext(context.Background(), repository.Rebind(cfg.Database.Driver, `INSERT INTO users
		(id, username, password_hash, role, status) VALUES ('test-admin', 'test-admin', 'test-only-no-login', 'admin', 'active')`)); err != nil {
		db.Close()
		t.Fatalf("创建测试管理员失败：%v", err)
	}
	if _, err := db.DB.ExecContext(context.Background(), repository.Rebind(cfg.Database.Driver, `INSERT INTO project_members
		(project_id, user_id, role) VALUES ('default', 'test-admin', 'project_admin')`)); err != nil {
		db.Close()
		t.Fatalf("创建测试项目成员失败：%v", err)
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
		ProjectMembers:   repository.NewSQLProjectMemberRepository(db.DB, cfg.Database.Driver),
		ProjectResources: repository.NewSQLProjectMemberRepository(db.DB, cfg.Database.Driver),
		Users:            repository.NewSQLUserRepository(db.DB, cfg.Database.Driver),
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
	if err != nil || len(logs) != 2 || logs[0].Status != "success" || logs[1].Status != "running" {
		t.Fatalf("审计日志记录错误：%+v %v", logs, err)
	}
	wantScope := repository.HistoryScope{ScopeType: repository.ScopeProject, ProjectID: domain.DefaultProjectID}
	if items[0].HistoryScope != wantScope || attempts[0].HistoryScope != wantScope || logs[0].HistoryScope != wantScope {
		t.Fatalf("请求任务、尝试与审计范围不一致：%+v %+v %+v", items[0], attempts[0], logs[0])
	}
	if items[0].OperatorID != "test-admin" || logs[0].OperatorID != "test-admin" || logs[0].TaskID != items[0].ID {
		t.Fatalf("服务端会话操作者或审计任务关联缺失：%+v %+v", items[0], logs[0])
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

func TestIssueTaskRequiresDeploymentPermission(t *testing.T) {
	calls := 0
	server, _, _, closeDB := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		calls++
		return certificate.IssueResult{}, deployment.Report{}, nil
	})
	defer closeDB()

	server.projectMembers = permissionProjectMemberRepository{
		ProjectMemberRepository: server.projectMembers,
		allow: func(permission domain.ProjectPermission) error {
			if permission == domain.PermissionDeploymentExecute {
				return repository.ErrProjectAccessDenied
			}
			return nil
		},
	}

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("缺少部署权限时证书任务未拒绝：%d %s", response.Code, response.Body.String())
	}
	if calls != 0 {
		t.Fatalf("权限拒绝后仍调用证书申请工作流：%d", calls)
	}
}

type permissionProjectMemberRepository struct {
	repository.ProjectMemberRepository
	allow func(domain.ProjectPermission) error
}

func (p permissionProjectMemberRepository) AuthorizeProject(_ context.Context, _ string, _ string, permission domain.ProjectPermission) error {
	return p.allow(permission)
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

type failingTaskStartAuditRepository struct {
	repository.AuditRepository
	failure error
}

func (r failingTaskStartAuditRepository) CreateAuditLog(ctx context.Context, item repository.AuditLog) error {
	if item.Status == "running" {
		return r.failure
	}
	return r.AuditRepository.CreateAuditLog(ctx, item)
}

func TestTaskAPIClosesTaskWhenStartAuditFails(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
		t.Fatal("任务启动审计失败时不应执行证书申请")
		return certificate.IssueResult{}, deployment.Report{}, nil
	})
	defer closeDB()
	server.audit = failingTaskStartAuditRepository{
		AuditRepository: auditRepo,
		failure:         errors.New("模拟任务启动审计失败"),
	}

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "任务启动审计记录失败") {
		t.Fatalf("任务启动审计失败响应错误：%d %s", response.Code, response.Body.String())
	}
	items, err := taskRepo.ListTasks(context.Background(), "", 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" || items[0].Stage != "record" {
		t.Fatalf("任务启动审计失败后任务未收敛：%+v %v", items, err)
	}
	attempts, err := taskRepo.ListAttempts(context.Background(), items[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" {
		t.Fatalf("任务启动审计失败后尝试未收敛：%+v %v", attempts, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 0 {
		t.Fatalf("启动审计写入失败时不应生成伪造终态审计：%+v %v", logs, err)
	}
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
	if err != nil || len(logs) != 2 || logs[0].Status != "error" || logs[1].Status != "running" {
		t.Fatalf("失败审计状态错误：%+v %v", logs, err)
	}
}

func TestTaskAPIRejectsUnknownTask(t *testing.T) {
	server, _, _, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/unknown", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("未知任务或无权任务应返回 403：%d %s", response.Code, response.Body.String())
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
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(ctx context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
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
	logs, err := auditRepo.ListAuditLogs(t.Context(), 10)
	if err != nil || len(logs) != 3 {
		t.Fatalf("取消和申请收尾审计缺失：%+v，%v", logs, err)
	}
	for _, entry := range logs {
		if entry.TaskID != item.ID || entry.OperatorID != "test-admin" || entry.HistoryScope != item.HistoryScope {
			t.Fatalf("取消审计关联错误：%+v", entry)
		}
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

type taskPermissionProjectMemberRepository struct {
	repository.ProjectMemberRepository
	authorize func(context.Context, string, string, domain.ProjectPermission) error
}

func (r taskPermissionProjectMemberRepository) AuthorizeProject(ctx context.Context, actorID, projectID string, permission domain.ProjectPermission) error {
	if r.authorize != nil {
		return r.authorize(ctx, actorID, projectID, permission)
	}
	return nil
}

func TestTaskAPIDeniesCancelWithoutProjectPermission(t *testing.T) {
	server, tasks, _, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	server.projectMembers = taskPermissionProjectMemberRepository{
		ProjectMemberRepository: server.projectMembers,
		authorize: func(_ context.Context, _ string, _ string, permission domain.ProjectPermission) error {
			if permission == domain.PermissionTaskCancel {
				return repository.ErrProjectAccessDenied
			}
			return nil
		},
	}
	item := repository.Task{
		HistoryScope: repository.HistoryScope{ScopeType: repository.ScopeProject, ProjectID: domain.DefaultProjectID},
		ID:           "cancel-permission-task", DomainID: "domain-1", TaskType: "certificate_issue", Status: "running", Stage: "certificate_issue",
	}
	if err := tasks.CreateTask(t.Context(), item); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+item.ID, nil))
	stored, err := tasks.GetTask(t.Context(), item.ID)
	if response.Code != http.StatusForbidden || err != nil || stored.Status != "running" {
		t.Fatalf("cancel permission denial changed task: %d %+v %v", response.Code, stored, err)
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

func TestAuditLogResponseUsesDashboardFieldNames(t *testing.T) {
	server, _, auditRepo, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	ctx := context.Background()
	createdAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for _, status := range []string{"success", "error"} {
		if err := auditRepo.CreateAuditLog(ctx, repository.AuditLog{
			Action: "login", ResourceType: "auth", ResourceID: "admin",
			Status: status, Message: "login", CreatedAt: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?limit=2", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var payload struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 {
		t.Fatal(response.Body.String())
	}
	var previousID int64
	for i, item := range payload.Items {
		for _, field := range []string{"id", "action", "resource_type", "resource_id", "status", "message", "created_at", "scope_type", "project_id", "task_id", "operator_id"} {
			if _, ok := item[field]; !ok {
				t.Fatal(field, response.Body.String())
			}
		}
		if len(item) != 11 {
			t.Fatal(response.Body.String())
		}
		var id int64
		if err := json.Unmarshal(item["id"], &id); err != nil || id <= 0 || (i > 0 && id >= previousID) {
			t.Fatal(id, previousID, err)
		}
		previousID = id
		for field, expected := range map[string]string{
			"action": "login", "resource_type": "auth", "resource_id": "admin",
			"scope_type": "system", "project_id": "", "task_id": "", "operator_id": "",
			"status": []string{"error", "success"}[i], "message": "login", "created_at": createdAt.Format(time.RFC3339),
		} {
			var actual string
			if err := json.Unmarshal(item[field], &actual); err != nil || actual != expected {
				t.Fatal(field, actual, expected, err)
			}
		}
	}
}

func TestTaskAPIRetriesFailedCertificateIssue(t *testing.T) {
	calls := 0
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		calls++
		if calls == 1 {
			return certificate.IssueResult{DomainID: id}, deployment.Report{}, errors.New("temporary certificate failure")
		}
		return certificate.IssueResult{DomainID: id}, deployment.Report{Status: "success"}, nil
	})
	defer closeDB()

	first := httptest.NewRecorder()
	serveAdminHTTP(server, first, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if first.Code != http.StatusBadGateway {
		t.Fatalf("initial failure response: %d %s", first.Code, first.Body.String())
	}
	tasks, err := taskRepo.ListTasks(context.Background(), "domain-1", 10)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "failed" {
		t.Fatalf("failed task was not persisted: %+v %v", tasks, err)
	}

	retry := httptest.NewRecorder()
	serveAdminHTTP(server, retry, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+tasks[0].ID+"/retry", nil))
	if retry.Code != http.StatusOK {
		t.Fatalf("retry response: %d %s", retry.Code, retry.Body.String())
	}
	updated, err := taskRepo.GetTask(context.Background(), tasks[0].ID)
	if err != nil || updated.Status != "success" || updated.OperatorID != "test-admin" {
		t.Fatalf("retried task did not converge: %+v %v", updated, err)
	}
	attempts, err := taskRepo.ListAttempts(context.Background(), tasks[0].ID)
	if err != nil || len(attempts) != 2 || attempts[0].AttemptNumber != 1 || attempts[0].Status != "failed" || attempts[1].AttemptNumber != 2 || attempts[1].Status != "success" {
		t.Fatalf("retry attempts were not recorded correctly: %+v %v", attempts, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 4 || logs[0].Action != "certificate_issue_retry" || logs[0].Status != "success" || logs[1].Action != "certificate_issue_retry" || logs[1].Status != "running" || logs[0].OperatorID != "test-admin" {
		t.Fatalf("retry audit was not recorded correctly: %+v %v", logs, err)
	}
}

func TestTaskAPIRecordsPartialDeploymentSuccess(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{DomainID: id}, deployment.Report{
			Total: 2, Success: 1, Failed: 1, Status: "partial",
			Results: []deployment.Result{
				{ServerID: "server-success", Status: "success"},
				{ServerID: "server-failed", Status: "failed", Message: "deployment failed"},
			},
		}, nil
	})
	defer closeDB()

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("partial response: %d %s", response.Code, response.Body.String())
	}
	items, err := taskRepo.ListTasks(context.Background(), "domain-1", 10)
	if err != nil || len(items) != 1 || items[0].Status != "partial_success" {
		t.Fatalf("partial task status: %+v %v", items, err)
	}
	attempts, err := taskRepo.ListAttempts(context.Background(), items[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "partial_success" {
		t.Fatalf("partial attempt status: %+v %v", attempts, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 2 || logs[0].Status != "partial_success" || logs[1].Status != "running" {
		t.Fatalf("partial audit status: %+v %v", logs, err)
	}
}

func TestTaskAPIRecordsPartialDeploymentFailureAsPartialSuccess(t *testing.T) {
	server, taskRepo, auditRepo, closeDB := newTaskAPITestServer(t, func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
		return certificate.IssueResult{DomainID: id}, deployment.Report{
			Total: 2, Success: 1, Failed: 1, Status: "partial",
		}, errors.New("deployment partially failed")
	})
	defer closeDB()

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusMultiStatus {
		t.Fatalf("partial failure response: %d %s", response.Code, response.Body.String())
	}
	items, err := taskRepo.ListTasks(context.Background(), "domain-1", 10)
	if err != nil || len(items) != 1 || items[0].Status != "partial_success" {
		t.Fatalf("partial failure task status: %+v %v", items, err)
	}
	attempts, err := taskRepo.ListAttempts(context.Background(), items[0].ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "partial_success" {
		t.Fatalf("partial failure attempt status: %+v %v", attempts, err)
	}
	logs, err := auditRepo.ListAuditLogs(context.Background(), 10)
	if err != nil || len(logs) != 2 || logs[0].Status != "partial_success" || logs[1].Status != "running" {
		t.Fatalf("partial failure audit status: %+v %v", logs, err)
	}
}
