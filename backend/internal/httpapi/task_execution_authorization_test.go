package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAsyncExecutionRechecksQueuedAuthorization(t *testing.T) {
	for _, taskType := range []string{"certificate_issue", "certificate_deploy", "certificate_import", "certificate_validate"} {
		for _, scenario := range []string{
			"unchanged", "disabled_user", "removed_member", "read_only_member",
			"moved_domain", "deleted_domain", "deployment_denied", "certificate_denied",
			"dns_denied", "private_key_denied", "authorization_error", "missing_authorizer", "missing_reader",
		} {
			t.Run(taskType+"/"+scenario, func(t *testing.T) {
				var calls atomic.Int32
				operation := func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
					calls.Add(1)
					return certificate.IssueResult{}, deployment.Report{Status: "success"}, nil
				}
				server, tasks, audit, cleanup := newTaskAPITestServer(t, operation)
				defer cleanup()
				server.deployCertificateTask = operation
				server.validateCertificateTask = func(context.Context, string) (certificate.IssueResult, certificate.ValidationResult, error) {
					calls.Add(1)
					return certificate.IssueResult{}, certificate.ValidationResult{Domains: []string{"example.com"}}, nil
				}
				server.importCertificateTask = func(ctx context.Context, request certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
					return operation(ctx, request.DomainID)
				}
				server.asyncTasks = make(map[string]bool)
				server.taskSlots = make(chan struct{}, 1)
				server.taskSlots <- struct{}{}
				path := "/api/v1/domains/domain-1/issue"
				if taskType == "certificate_deploy" {
					path = "/api/v1/domains/domain-1/deploy"
				}
				if taskType == "certificate_import" {
					path = "/api/v1/domains/domain-1/certificate/import"
				}
				if taskType == "certificate_validate" {
					path = "/api/v1/domains/domain-1/certificate/validate"
				}
				response := httptest.NewRecorder()
				serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, path,
					strings.NewReader(`{"certificate_path":"/managed/cert.pem","private_key_path":"/managed/key.pem"}`)))
				task := submittedTask(t, response)
				if calls.Load() != 0 {
					t.Fatal("占满执行槽后任务仍执行")
				}
				db, err := database.Open(t.Context(), server.config.Database)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var statements []string
				switch scenario {
				case "disabled_user":
					statements = []string{"UPDATE users SET status = 'disabled' WHERE id = 'test-admin'"}
				case "removed_member":
					statements = []string{"DELETE FROM project_members WHERE user_id = 'test-admin'"}
				case "read_only_member":
					statements = []string{"UPDATE project_members SET role = 'read_only' WHERE user_id = 'test-admin'"}
				case "moved_domain":
					// 保留两项目权限，确保拒绝来自归属变化而非不可见。
					statements = []string{
						"INSERT INTO projects (id, name, description) VALUES ('moved', '迁移后的项目', '')",
						"INSERT INTO project_members (project_id, user_id, role) VALUES ('moved', 'test-admin', 'project_admin')",
						"UPDATE domains SET project_id = 'moved' WHERE id = 'domain-1'",
					}
				case "deleted_domain":
					statements = []string{"DELETE FROM domains WHERE id = 'domain-1'"}
				case "missing_authorizer":
					server.projectMembers = nil
				case "missing_reader":
					server.projectResources = nil
				case "deployment_denied", "certificate_denied", "dns_denied", "private_key_denied", "authorization_error":
					server.projectMembers = permissionProjectMemberRepository{
						ProjectMemberRepository: server.projectMembers,
						allow: func(permission domain.ProjectPermission) error {
							if scenario == "authorization_error" {
								return errors.New("private-authorization-detail")
							}
							if scenario == "deployment_denied" && permission == domain.PermissionDeploymentExecute ||
								scenario == "certificate_denied" && permission == domain.PermissionCertificateManage ||
								scenario == "dns_denied" && permission == domain.PermissionDNSCredentialUse ||
								scenario == "private_key_denied" && permission == domain.PermissionPrivateKeyRead {
								return repository.ErrProjectAccessDenied
							}
							return nil
						},
					}
				}
				for _, statement := range statements {
					if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
				<-server.taskSlots
				waitTaskWorkersForTest(t, server)
				want := "failed"
				if taskType == "certificate_validate" && (scenario == "deployment_denied" || scenario == "certificate_denied" || scenario == "dns_denied") || taskType != "certificate_validate" && scenario == "private_key_denied" {
					want = "success"
				}
				if scenario == "unchanged" || taskType == "certificate_deploy" && (scenario == "dns_denied" || scenario == "certificate_denied") || taskType == "certificate_import" && scenario == "dns_denied" {
					want = "success"
				}
				stored, err := tasks.GetTask(t.Context(), task.ID)
				if err != nil || stored.Status != want {
					t.Fatalf("执行前权限变化未正确处理：%+v %v，外部调用 %d", stored, err, calls.Load())
				}
				attempts, err := tasks.ListAttempts(t.Context(), task.ID)
				if err != nil || len(attempts) != 1 || attempts[0].Status != want {
					t.Fatalf("尝试状态不一致：%+v %v", attempts, err)
				}
				if want == "success" {
					if calls.Load() != 1 {
						t.Fatal("正常授权必须执行一次")
					}
					return
				}
				if calls.Load() != 0 || stored.Stage != "authorize" || strings.Contains(stored.Message, "private-") {
					t.Fatalf("拒绝授权仍发生副作用或暴露内部错误：%+v，调用 %d", stored, calls.Load())
				}
				logs, err := audit.ListAuditLogs(t.Context(), 20)
				if err != nil {
					t.Fatal(err)
				}
				completed := 0
				for _, log := range logs {
					if log.Action == taskType && log.Status == "error" {
						completed++
					}
				}
				if completed != 1 {
					t.Fatalf("拒绝执行必须保留一次失败审计：%+v", logs)
				}
			})
		}
	}
}

func TestAsyncRetryRechecksCurrentOperatorAuthorization(t *testing.T) {
	for _, scenario := range []string{"unchanged", "disabled_retry_operator", "retry_denied", "certificate_denied"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			server, tasks, _, cleanup := newTaskAPITestServer(t, func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
				if calls.Add(1) == 1 {
					return certificate.IssueResult{}, deployment.Report{}, errors.New("首次申请失败")
				}
				return certificate.IssueResult{}, deployment.Report{Status: "success"}, nil
			})
			defer cleanup()
			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
			task := submittedTask(t, response)
			waitTaskWorkersForTest(t, server)
			db, err := database.Open(t.Context(), server.config.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, statement := range []string{
				"INSERT INTO users (id, username, password_hash, role, status) VALUES ('retry-admin', 'retry-admin', 'test-only-no-login', 'admin', 'active')",
				"INSERT INTO project_members (project_id, user_id, role) VALUES ('default', 'retry-admin', 'project_admin')",
				"UPDATE users SET status = 'disabled' WHERE id = 'test-admin'",
			} {
				if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			server.taskSlots <- struct{}{}
			server.taskSlots <- struct{}{}
			server.taskSlots <- struct{}{}
			server.taskSlots <- struct{}{}
			response = httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/retry", nil)
			request.AddCookie(httpUserSessionCookie(server, "retry-admin", repository.UserRoleAdmin))
			server.Handler().ServeHTTP(response, request)
			submittedTask(t, response)
			if scenario == "disabled_retry_operator" {
				if _, err := db.DB.ExecContext(t.Context(), "UPDATE users SET status = 'disabled' WHERE id = 'retry-admin'"); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "unchanged" {
				server.projectMembers = permissionProjectMemberRepository{
					ProjectMemberRepository: server.projectMembers,
					allow: func(permission domain.ProjectPermission) error {
						if scenario == "retry_denied" && permission == domain.PermissionTaskRetry ||
							scenario == "certificate_denied" && permission == domain.PermissionCertificateManage {
							return repository.ErrProjectAccessDenied
						}
						return nil
					},
				}
			}
			<-server.taskSlots
			waitTaskWorkersForTest(t, server)
			want, wantCalls := "failed", int32(1)
			if scenario == "unchanged" {
				want, wantCalls = "success", 2
			}
			stored, err := tasks.GetTask(t.Context(), task.ID)
			if err != nil || stored.OperatorID != "retry-admin" || stored.Status != want || calls.Load() != wantCalls {
				t.Fatalf("重试未使用本次操作者实时权限：%+v %v，调用 %d", stored, err, calls.Load())
			}
			attempts, err := tasks.ListAttempts(t.Context(), task.ID)
			if err != nil || len(attempts) != 2 {
				t.Fatalf("重试记录缺失：%+v %v", attempts, err)
			}
			for _, attempt := range attempts {
				if attempt.AttemptNumber == 2 && attempt.Status != want {
					t.Fatalf("重试终态不一致：%+v", attempt)
				}
			}
		})
	}
}
