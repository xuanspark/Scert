package httpapi

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
)

func restoreRequest(key string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/versions/history-1/restore", nil)
	request.Header.Set("Idempotency-Key", key)
	return request
}

func TestAsyncRestoreSubmitsPersistedTaskWithoutNestedWorkflow(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	var gotDomain, gotVersion string
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.restoreVersion = func(context.Context, string, string) (certificate.IssueResult, error) {
		t.Fatal("异步恢复不得调用兼容同步工作流")
		return certificate.IssueResult{}, nil
	}
	server.restoreVersionTask = func(ctx context.Context, domainID, version string) (certificate.IssueResult, error) {
		calls.Add(1)
		gotDomain, gotVersion = domainID, version
		close(started)
		select {
		case <-release:
			return certificate.IssueResult{DomainID: domainID}, nil
		case <-ctx.Done():
			return certificate.IssueResult{}, ctx.Err()
		}
	}

	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, restoreRequest("restore-async-1"))
	task := submittedTask(t, response)
	if task.TaskType != "certificate_restore" {
		t.Fatalf("恢复任务类型错误：%+v", task)
	}
	if response.Header().Get("Location") != "/api/v1/tasks/"+task.ID {
		t.Fatalf("恢复任务查询地址错误：%s", response.Header().Get("Location"))
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("恢复后台任务未开始执行")
	}
	close(release)
	waitTaskWorkersForTest(t, server)

	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "success" {
		t.Fatalf("恢复任务未成功收敛：%+v %v", stored, err)
	}
	if gotDomain != "domain-1" || gotVersion != "history-1" || calls.Load() != 1 {
		t.Fatalf("恢复参数或执行次数错误：domain=%q version=%q calls=%d", gotDomain, gotVersion, calls.Load())
	}
	if !strings.Contains(stored.Message, "本地证书历史版本恢复完成") || !strings.Contains(stored.Message, "远端服务器部署完成") {
		t.Fatalf("恢复任务状态文案未说明本地与远端边界：%s", stored.Message)
	}
}

func TestAsyncRestoreIdempotencyAndActiveTaskConflict(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.restoreVersionTask = func(ctx context.Context, domainID, _ string) (certificate.IssueResult, error) {
		calls.Add(1)
		select {
		case <-release:
			return certificate.IssueResult{DomainID: domainID}, nil
		case <-ctx.Done():
			return certificate.IssueResult{}, ctx.Err()
		}
	}

	first := httptest.NewRecorder()
	serveAdminHTTP(server, first, restoreRequest("restore-duplicate"))
	task := submittedTask(t, first)
	replay := httptest.NewRecorder()
	serveAdminHTTP(server, replay, restoreRequest("restore-duplicate"))
	if replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), task.ID) {
		t.Fatalf("活动恢复任务幂等重放未被拒绝：%d %s", replay.Code, replay.Body.String())
	}
	conflict := httptest.NewRecorder()
	serveAdminHTTP(server, conflict, restoreRequest("restore-other"))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("同域名活动恢复任务未被拒绝：%d %s", conflict.Code, conflict.Body.String())
	}
	close(release)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "success" || calls.Load() != 1 {
		t.Fatalf("幂等或冲突检查影响恢复任务：%+v %v calls=%d", stored, err, calls.Load())
	}
}

func TestAsyncRestoreFailureDoesNotExposePrivatePath(t *testing.T) {
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.restoreVersionTask = func(context.Context, string, string) (certificate.IssueResult, error) {
		return certificate.IssueResult{}, errors.New("读取私钥失败：/etc/scert/private/live/key.pem")
	}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, restoreRequest("restore-failure"))
	task := submittedTask(t, response)
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("恢复失败任务未收敛：%+v %v", stored, err)
	}
	if strings.Contains(stored.Message, "/etc/scert/private") || strings.Contains(response.Body.String(), "/etc/scert/private") {
		t.Fatal("恢复失败结果泄露私钥路径")
	}
}

func TestAsyncRestoreCancellationStopsWorkflow(t *testing.T) {
	started := make(chan struct{})
	server, tasks, _, cleanup := newTaskAPITestServer(t, nil)
	defer cleanup()
	server.restoreVersionTask = func(ctx context.Context, domainID, _ string) (certificate.IssueResult, error) {
		close(started)
		<-ctx.Done()
		return certificate.IssueResult{DomainID: domainID}, ctx.Err()
	}
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, restoreRequest("restore-cancel"))
	task := submittedTask(t, response)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("恢复任务未开始执行")
	}
	cancel := httptest.NewRecorder()
	serveAdminHTTP(server, cancel, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+task.ID, nil))
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("取消恢复任务失败：%d %s", cancel.Code, cancel.Body.String())
	}
	waitTaskWorkersForTest(t, server)
	stored, err := tasks.GetTask(t.Context(), task.ID)
	if err != nil || stored.Status != "cancelled" {
		t.Fatalf("恢复任务取消后状态错误：%+v %v", stored, err)
	}
}
