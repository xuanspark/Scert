package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestTaskAPICancelsTrackedCertificateWorkflowTask(t *testing.T) {
	server, tasks, _, closeDB := newTaskAPITestServer(t, nil)
	defer closeDB()
	item := repository.Task{
		ID: "tracked-workflow-task", DomainID: "domain-1",
		HistoryScope: repository.HistoryScope{ScopeType: repository.ScopeProject, ProjectID: domain.DefaultProjectID},
		TaskType:     "certificate_deploy", Status: "running", Stage: "certificate_deploy",
	}
	if err := tasks.CreateTask(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	cancelled := make(chan struct{}, 1)
	server.registerTaskCancel(item.ID, func() { cancelled <- struct{}{} })
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+item.ID, nil))
	stored, err := tasks.GetTask(t.Context(), item.ID)
	if response.Code != http.StatusOK || err != nil || stored.Status != "cancelled" {
		t.Fatalf("tracked workflow task was not cancelled: %d %+v %v", response.Code, stored, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("tracked workflow cancellation callback was not called")
	}
}

func TestTaskAPICanCancelRetryWaitAndCancellingTasks(t *testing.T) {
	for _, status := range []string{"retry_wait", "cancelling"} {
		t.Run(status, func(t *testing.T) {
			server, tasks, _, closeDB := newTaskAPITestServer(t, nil)
			defer closeDB()
			started := time.Now().UTC().Add(-time.Minute)
			item := repository.Task{
				ID: "cancel-" + status, DomainID: "domain-1",
				HistoryScope: repository.HistoryScope{ScopeType: repository.ScopeProject, ProjectID: domain.DefaultProjectID},
				TaskType:     "certificate_deploy", Status: status, Stage: "certificate_deploy",
				StartedAt: &started,
			}
			if err := tasks.CreateTask(t.Context(), item); err != nil {
				t.Fatal(err)
			}
			attempt := repository.TaskAttempt{
				ID: "attempt-" + status, TaskID: item.ID, AttemptNumber: 1,
				Stage: item.Stage, Status: status, Message: "等待执行", StartedAt: &started, CreatedAt: started,
			}
			if err := tasks.CreateAttempt(t.Context(), attempt); err != nil {
				t.Fatal(err)
			}

			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+item.ID, nil))
			stored, err := tasks.GetTask(t.Context(), item.ID)
			if response.Code != http.StatusOK || err != nil || stored.Status != "cancelled" {
				t.Fatalf("%s 任务未能取消：%d %+v %v", status, response.Code, stored, err)
			}
			attempts, err := tasks.ListAttempts(t.Context(), item.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != "cancelled" {
				t.Fatalf("%s 任务尝试状态未同步取消：%+v %v", status, attempts, err)
			}
		})
	}
}

func TestTaskAPICannotFalselyCancelWorkflowTasks(t *testing.T) {
	for _, kind := range []string{"renewal_run"} {
		t.Run(kind, func(t *testing.T) {
			server, tasks, _, closeDB := newTaskAPITestServer(t, nil)
			defer closeDB()
			item := repository.Task{ID: "workflow-task", DomainID: "domain-1", TaskType: kind, Status: "running", Stage: "certificate_issue"}
			if err := tasks.CreateTask(t.Context(), item); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+item.ID, nil))
			stored, err := tasks.GetTask(t.Context(), item.ID)
			if response.Code != http.StatusConflict || err != nil || stored.Status != "running" {
				t.Fatalf("工作流任务被伪造为已取消：%d %+v %v", response.Code, stored, err)
			}
		})
	}
}
