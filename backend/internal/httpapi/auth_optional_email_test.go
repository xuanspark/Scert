package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestAuthSessionsSupportOptionalEmail(t *testing.T) {
	for _, role := range []string{repository.UserRoleUser, repository.UserRoleAdmin} {
		t.Run(role, func(t *testing.T) {
			directory := t.TempDir()
			cfg := readyTestConfig(directory)
			cfg.Database.DSN = filepath.Join(directory, "optional-email.db")
			db, err := database.Open(t.Context(), cfg.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
				t.Fatal(err)
			}
			users := repository.NewSQLUserRepository(db.DB, "sqlite")
			user := repository.User{
				ID: "optional-email", Username: "optional-email",
				PasswordHash: httpUserPasswordHash(t, "Operator123"),
				Role:         role, Status: repository.UserStatusActive,
			}
			if err := users.CreateUser(t.Context(), user); err != nil {
				t.Fatal(err)
			}
			server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, Users: users})
			login := loginAuthTest(t, server, user.Username, "Operator123", false)
			cookies := login.Result().Cookies()
			if len(cookies) == 0 {
				t.Fatal("missing session cookie")
			}
			loginAuthTest(t, server, user.Username, "Operator123", false)
			response := authSessionRequest(server, cookies[0])
			if response.Code != http.StatusOK {
				t.Fatalf("optional-email session: status=%d body=%s", response.Code, response.Body.String())
			}
			var payload struct {
				Authenticated bool   `json:"authenticated"`
				Email         string `json:"email"`
				Role          string `json:"role"`
				IsAdmin       bool   `json:"is_admin"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if !payload.Authenticated || payload.Email != "" || payload.Role != role || payload.IsAdmin != (role == repository.UserRoleAdmin) {
				t.Fatalf("unexpected session identity: %+v", payload)
			}
			// 服务商目录仅含公共字段定义，已登录用户可读；系统设置仍仅超级管理员可读。
			settings := serveUserRequest(server, cookies[0], http.MethodGet, "/api/v1/settings", "")
			if settings.Code != http.StatusForbidden {
				t.Fatalf("非超级管理员读取系统设置应返回 403，实际为 %d", settings.Code)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/dns/providers", nil)
			request.AddCookie(cookies[0])
			access := httptest.NewRecorder()
			server.Handler().ServeHTTP(access, request)
			expectedStatus := http.StatusOK
			if access.Code != expectedStatus {
				t.Fatalf("role boundary: status=%d want=%d", access.Code, expectedStatus)
			}
			user.Status = repository.UserStatusDisabled
			if err := users.UpdateUser(t.Context(), user); err != nil {
				t.Fatal(err)
			}
			disabled := authSessionRequest(server, cookies[0])
			if disabled.Code != http.StatusUnauthorized {
				t.Fatalf("disabled session: status=%d", disabled.Code)
			}
		})
	}
}
