package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

func TestDomainResourceScopeAPI(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"creator", "editor"} {
		if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO users (id, username, password_hash, role, status) VALUES (?, ?, 'test-only-no-login', 'super_admin', 'active')`, userID, userID); err != nil {
			t.Fatal(err)
		}
	}
	repo := repository.NewSQLDomainRepository(db.DB)
	server := New(Dependencies{Config: cfg, Repository: repo, ProjectDomains: repository.NewSQLProjectMemberRepository(db.DB)})
	request := func(method, path, body, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(httpUserSessionCookie(server, user, repository.UserRoleAdmin))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response
	}
	for _, field := range []string{"project_id", "owner_id", "created_by", "updated_by"} {
		body := fmt.Sprintf(`{"name":"forged.example.test","certificate_type":"single","%s":"forged"}`, field)
		response := request(http.MethodPost, "/api/v1/domains", body, "creator")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("客户端不应提供 %s：%d", field, response.Code)
		}
	}
	response := request(http.MethodPost, "/api/v1/domains", `{"name":"scope.example.test","certificate_type":"single"}`, "creator")
	if response.Code != http.StatusCreated {
		t.Fatalf("创建失败：%d，%s", response.Code, response.Body.String())
	}
	var created domain.Domain
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	want := domain.ResourceScope{ProjectID: "default", OwnerID: "creator", CreatedBy: "creator", UpdatedBy: "creator"}
	if created.ResourceScope != want {
		t.Fatalf("创建响应归属错误：%+v", created.ResourceScope)
	}
	response = request(http.MethodPut, "/api/v1/domains/"+created.ID, `{"name":"edited.example.test","certificate_type":"single"}`, "editor")
	if response.Code != http.StatusOK {
		t.Fatalf("更新失败：%d，%s", response.Code, response.Body.String())
	}
	loaded, err := repo.GetByID(t.Context(), created.ID)
	want.UpdatedBy = "editor"
	if err != nil || loaded.ResourceScope != want {
		t.Fatalf("更新后归属错误：%+v，%v", loaded.ResourceScope, err)
	}
}

func TestResourceScopePreservesProjectAndCreator(t *testing.T) {
	server := New(Dependencies{Config: readyTestConfig(t.TempDir())})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	ctx.Request.AddCookie(httpUserSessionCookie(server, "editor", repository.UserRoleAdmin))
	existing := domain.ResourceScope{ProjectID: "project-a", OwnerID: "owner", CreatedBy: "creator", UpdatedBy: "previous"}
	updated := server.updatedResourceScope(ctx, existing)
	if updated.ProjectID != existing.ProjectID || updated.OwnerID != existing.OwnerID || updated.CreatedBy != existing.CreatedBy || updated.UpdatedBy != "editor" {
		t.Fatalf("更新操作者时丢失原资源归属：%+v", updated)
	}
}
