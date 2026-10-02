package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func importUploadRequest(t *testing.T, key string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for _, name := range []string{"certificate", "private_key"} {
		part, err := writer.CreateFormFile(name, name+".pem")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", key)
	return request
}

func TestAsyncImportUploadLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "saved_failure", "partial", "panic", "cancel", "shutdown", "stopped", "full", "invalid_key", "replay"} {
		t.Run(scenario, func(t *testing.T) {
			temporary := t.TempDir()
			for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(name, temporary)
			}
			var calls atomic.Int32
			server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
			defer cleanup()
			server.importCertificateTask = func(_ context.Context, request certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
				calls.Add(1)
				for path, expected := range map[string]string{request.Certificate: "certificate", request.PrivateKey: "private_key"} {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != expected {
						t.Errorf("上传文件被提前回收或内容变化：%v", err)
					}
					info, err := os.Stat(path)
					if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
						t.Errorf("上传文件权限错误：%v", err)
					}
				}
				if scenario == "panic" {
					panic("private-import-error")
				}
				if scenario == "failure" {
					return certificate.IssueResult{}, deployment.Report{}, errors.New("private-import-error")
				}
				result := certificate.IssueResult{DomainID: request.DomainID, Saved: certificate.SavedResource{
					CertificatePath: "/private/cert.pem", FullChainPath: "/private/fullchain.pem", PrivateKeyPath: "/private/key.pem",
				}}
				if scenario == "saved_failure" || scenario == "partial" {
					status := "failed"
					if scenario == "partial" {
						status = "partial"
					}
					return result, deployment.Report{Status: status}, errors.New("private-deploy-error")
				}
				return result, deployment.Report{Status: "success"}, nil
			}
			server.asyncTasks = make(map[string]bool)
			server.taskSlots = make(chan struct{}, 1)
			server.taskSlots <- struct{}{}
			key := "import-lifecycle"
			switch scenario {
			case "stopped":
				server.stopTaskWorkers()
			case "full":
				for index := range 32 {
					server.asyncTasks[string(rune('a'+index))] = true
				}
			case "invalid_key":
				key = strings.Repeat("x", 300)
			}
			response := httptest.NewRecorder()
			request := importUploadRequest(t, key)
			requestCtx, cancelRequest := context.WithCancel(request.Context())
			serveAdminHTTP(server, response, request.WithContext(requestCtx))
			cancelRequest()
			files, err := filepath.Glob(filepath.Join(temporary, "scert-upload-*"))
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stopped" || scenario == "full" || scenario == "invalid_key" {
				want := http.StatusServiceUnavailable
				if scenario == "invalid_key" {
					want = http.StatusBadRequest
				}
				if response.Code != want || len(files) != 0 || calls.Load() != 0 {
					t.Fatalf("拒绝提交仍保留上传文件或执行导入：%d %v %d", response.Code, files, calls.Load())
				}
				return
			}
			task := submittedTask(t, response)
			if task.TaskType != "certificate_import" || len(files) != 2 || calls.Load() != 0 {
				t.Fatalf("排队状态或上传文件所有权错误：%+v %v %d", task, files, calls.Load())
			}
			if scenario == "replay" {
				replay := httptest.NewRecorder()
				serveAdminHTTP(server, replay, importUploadRequest(t, key))
				remaining, _ := filepath.Glob(filepath.Join(temporary, "scert-upload-*"))
				if replay.Code != http.StatusConflict || len(remaining) != 2 {
					t.Fatalf("重复提交未回收本次上传文件：%d %v", replay.Code, remaining)
				}
			}
			switch scenario {
			case "cancel":
				cancelled := httptest.NewRecorder()
				serveAdminHTTP(server, cancelled, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
				if cancelled.Code != http.StatusAccepted {
					t.Fatalf("取消排队导入失败：%d %s", cancelled.Code, cancelled.Body.String())
				}
			case "shutdown":
				server.stopTaskWorkers()
			default:
				<-server.taskSlots
			}
			waitTaskWorkersForTest(t, server)
			wantStatus := "success"
			wantCalls := int32(1)
			switch scenario {
			case "failure", "saved_failure", "panic":
				wantStatus = "failed"
			case "partial":
				wantStatus = "partial_success"
			case "cancel", "shutdown":
				wantStatus, wantCalls = "cancelled", 0
			}
			stored, err := tasks.GetTask(t.Context(), task.ID)
			if err != nil || stored.Status != wantStatus || calls.Load() != wantCalls {
				t.Fatalf("导入终态错误：%+v %v，执行次数 %d", stored, err, calls.Load())
			}
			for _, path := range files {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("上传文件未回收：%v", err)
				}
			}
			if scenario == "saved_failure" || scenario == "partial" || scenario == "success" || scenario == "failure" {
				reportResponse := httptest.NewRecorder()
				serveAdminHTTP(server, reportResponse, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID+"/deployment-results", nil))
				var payload struct {
					Reports []struct {
						Report deployment.Report `json:"report"`
					} `json:"reports"`
				}
				if err := json.Unmarshal(reportResponse.Body.Bytes(), &payload); err != nil || len(payload.Reports) != 1 {
					t.Fatalf("完整导入结果未持久化：%s %v", reportResponse.Body.String(), err)
				}
				if payload.Reports[0].Report.CertificateImported != (scenario != "failure") {
					t.Fatalf("导入是否保存的状态丢失：%+v", payload)
				}
				if strings.Contains(reportResponse.Body.String(), "private-") || strings.Contains(reportResponse.Body.String(), "/private/") || strings.Contains(stored.Message, "private-") {
					t.Fatal("公开结果泄露内部错误或证书路径")
				}
				if scenario == "saved_failure" || scenario == "partial" {
					if !strings.Contains(stored.Message, "无需重新导入") {
						t.Fatalf("已保存证书的失败结果不明确：%s", stored.Message)
					}
				}
			}
			if scenario == "failure" {
				retry := httptest.NewRecorder()
				serveAdminHTTP(server, retry, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/retry", nil))
				if retry.Code != http.StatusConflict || calls.Load() != 1 {
					t.Fatal("不得重放已回收上传文件的导入任务")
				}
			}
		})
	}
}

func TestAsyncImportRejectedSubmissionCleansFiles(t *testing.T) {
	server, _, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	var cleaned atomic.Int32
	server.tasks = nil
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	server.submitPreparedDomainTask(ctx, "certificate_import", &preparedCertificateTask{cleanup: func() { cleaned.Add(1) }})
	if response.Code != http.StatusNotImplemented || cleaned.Load() != 1 {
		t.Fatal("缺失任务仓储时必须回收上传文件")
	}
}
