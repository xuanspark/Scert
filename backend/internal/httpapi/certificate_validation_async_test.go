package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAsyncValidationLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "panic", "cancel", "shutdown", "replay", "stopped", "full"} {
		t.Run(scenario, func(t *testing.T) {
			server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
			defer cleanup()
			var calls atomic.Int32
			server.validateCertificateTask = func(ctx context.Context, id string) (certificate.IssueResult, certificate.ValidationResult, error) {
				calls.Add(1)
				if ctx.Err() != nil || id != "domain-1" {
					t.Error("后台校验沿用已断开的请求或错误域名")
				}
				if scenario == "panic" {
					panic("private-validation-detail")
				}
				if scenario == "failure" {
					return certificate.IssueResult{}, certificate.ValidationResult{}, errors.New("private-validation-detail")
				}
				return certificate.IssueResult{Saved: certificate.SavedResource{PrivateKeyPath: "/private-path/key.pem"}},
					certificate.ValidationResult{Domains: []string{"example.com"}, FingerprintSHA256: "validated-fingerprint", ChainLength: 2}, nil
			}
			server.asyncTasks = make(map[string]bool)
			server.taskSlots = make(chan struct{}, 1)
			server.taskSlots <- struct{}{}
			if scenario == "stopped" {
				server.stopTaskWorkers()
			}
			if scenario == "full" {
				for index := range 32 {
					server.asyncTasks[string(rune('a'+index))] = true
				}
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/validate", nil)
			request.Header.Set("Idempotency-Key", "validation-request")
			ctx, cancel := context.WithCancel(request.Context())
			serveAdminHTTP(server, response, request.WithContext(ctx))
			cancel()
			if scenario == "stopped" || scenario == "full" {
				if response.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
					t.Fatalf("服务拒绝后仍执行校验：%d %d", response.Code, calls.Load())
				}
				return
			}
			task := submittedTask(t, response)
			if task.TaskType != "certificate_validate" || calls.Load() != 0 {
				t.Fatalf("校验没有正确排队：%+v %d", task, calls.Load())
			}
			if scenario == "replay" {
				replayed := httptest.NewRecorder()
				serveAdminHTTP(server, replayed, request)
				if replayed.Code != http.StatusConflict || !strings.Contains(replayed.Body.String(), task.ID) || !strings.Contains(replayed.Body.String(), `"replayed":true`) {
					t.Fatalf("校验重复请求没有复用原任务：%d %s", replayed.Code, replayed.Body.String())
				}
			}
			switch scenario {
			case "cancel":
				cancelled := httptest.NewRecorder()
				serveAdminHTTP(server, cancelled, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
				if cancelled.Code != http.StatusAccepted {
					t.Fatalf("取消排队校验失败：%d", cancelled.Code)
				}
			case "shutdown":
				server.stopTaskWorkers()
			default:
				<-server.taskSlots
			}
			waitTaskWorkersForTest(t, server)
			want, wantCalls := "success", int32(1)
			if scenario == "failure" || scenario == "panic" {
				want = "failed"
			}
			if scenario == "cancel" || scenario == "shutdown" {
				want, wantCalls = "cancelled", 0
			}
			stored, err := tasks.GetTask(t.Context(), task.ID)
			if err != nil || stored.Status != want || calls.Load() != wantCalls || strings.Contains(stored.Message, "private-") {
				t.Fatalf("校验终态或调用次数错误：%+v %v %d", stored, err, calls.Load())
			}
			result := httptest.NewRecorder()
			serveAdminHTTP(server, result, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID+"/validation-results", nil))
			var body struct {
				Items []repository.TaskValidationResult `json:"items"`
			}
			if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &body) != nil || strings.Contains(result.Body.String(), "private-") {
				t.Fatalf("读取校验结果失败或泄露内部内容：%d %s", result.Code, result.Body.String())
			}
			if want == "success" {
				if len(body.Items) != 1 || body.Items[0].Validation.FingerprintSHA256 != "validated-fingerprint" {
					t.Fatalf("后台校验详情丢失：%+v", body)
				}
			} else if len(body.Items) != 0 {
				t.Fatal("未通过校验却保存通过结果")
			}
			reports, err := tasks.ListTaskDeploymentReports(t.Context(), task.ID)
			if err != nil || len(reports) != 0 {
				t.Fatalf("校验生成伪部署报告：%+v %v", reports, err)
			}
		})
	}
}

func TestAsyncValidationRequiresOnlyExistingPrivateKeyPermission(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		server, _, _, cleanup := newTaskAPITestServer(t, nil)
		server.validateCertificateTask = func(context.Context, string) (certificate.IssueResult, certificate.ValidationResult, error) {
			return certificate.IssueResult{}, certificate.ValidationResult{Domains: []string{"example.com"}}, nil
		}
		server.projectMembers = permissionProjectMemberRepository{
			ProjectMemberRepository: server.projectMembers,
			allow: func(permission domain.ProjectPermission) error {
				if allowed && permission == domain.PermissionPrivateKeyRead {
					return nil
				}
				return repository.ErrProjectAccessDenied
			},
		}
		response := httptest.NewRecorder()
		serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/validate", nil))
		want := http.StatusForbidden
		if allowed {
			want = http.StatusAccepted
		}
		if response.Code != want {
			t.Fatalf("校验改变了原有权限要求：%d %s", response.Code, response.Body.String())
		}
		waitTaskWorkersForTest(t, server)
		cleanup()
	}
}
