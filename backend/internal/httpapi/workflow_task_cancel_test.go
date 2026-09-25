package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestTaskAPICannotFalselyCancelWorkflowTasks(t *testing.T) {
	for _, kind := range []string{"renewal_run", "certificate_deploy"} {
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
