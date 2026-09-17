package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestUserManagementAuthorizationAndCRUD(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.Database.DSN = filepath.Join(directory, "data", "users-api.db")
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatalf("打开用户 API 测试数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移用户 API 测试数据库失败：%v", err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email,
		PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatalf("保存用户 API 测试超级管理员失败：%v", err)
	}
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, Users: users})
	overSized := `{"username":"operator","password":"` + strings.Repeat("x", 70*1024) + `"}`
	overSizedResponse := serveUserRequest(server, httpUserSessionCookie(server, "primary-super-admin", repository.UserRoleSuperAdmin), http.MethodPost, "/api/v1/users", overSized)
	if overSizedResponse.Code != http.StatusBadRequest {
		t.Fatalf("oversized user request should return 400: %d %s", overSizedResponse.Code, overSizedResponse.Body.String())
	}
	unauthorized := serveUserRequest(server, nil, http.MethodGet, "/api/v1/users", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("未登录请求应返回 401：%d %s", unauthorized.Code, unauthorized.Body.String())
	}
	regularCookie := httpUserSessionCookie(server, "user-1", repository.UserRoleUser)
	forbidden := serveUserRequest(server, regularCookie, http.MethodGet, "/api/v1/users", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("普通用户请求应返回 403：%d %s", forbidden.Code, forbidden.Body.String())
	}

	adminCookie := httpUserSessionCookie(server, "primary-super-admin", repository.UserRoleSuperAdmin)
	created := serveUserRequest(server, adminCookie, http.MethodPost, "/api/v1/users", `{"username":"operator","email":"operator@example.test","password":"UserPass123","role":"user","status":"active"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("创建用户失败：%d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "password_hash") || strings.Contains(created.Body.String(), "UserPass123") {
		t.Fatalf("创建用户响应泄露敏感字段：%s", created.Body.String())
	}
	var createdUser repository.User
	if err := json.Unmarshal(created.Body.Bytes(), &createdUser); err != nil || createdUser.ID == "" {
		t.Fatalf("解析创建用户响应失败：%v %s", err, created.Body.String())
	}

	listed := serveUserRequest(server, adminCookie, http.MethodGet, "/api/v1/users", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "operator") {
		t.Fatalf("查询用户失败：%d %s", listed.Code, listed.Body.String())
	}
	updated := serveUserRequest(server, adminCookie, http.MethodPut, "/api/v1/users/"+createdUser.ID, `{"username":"operator","email":"operator@example.test","role":"admin","status":"disabled"}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "admin") {
		t.Fatalf("更新用户失败：%d %s", updated.Code, updated.Body.String())
	}

	loginDisabled := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"operator","password":"UserPass123"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(loginDisabled, loginRequest)
	if loginDisabled.Code != http.StatusUnauthorized {
		t.Fatalf("禁用用户登录应被拒绝：%d %s", loginDisabled.Code, loginDisabled.Body.String())
	}

	secondSuperAdmin := serveUserRequest(server, adminCookie, http.MethodPost, "/api/v1/users", `{"username":"owner","password":"OwnerPass123","role":"super_admin","status":"active"}`)
	if secondSuperAdmin.Code != http.StatusBadRequest {
		t.Fatalf("新增第二个超级管理员应被拒绝：%d %s", secondSuperAdmin.Code, secondSuperAdmin.Body.String())
	}
	deleted := serveUserRequest(server, adminCookie, http.MethodDelete, "/api/v1/users/"+createdUser.ID, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("删除普通管理员失败：%d %s", deleted.Code, deleted.Body.String())
	}
	protected := serveUserRequest(server, adminCookie, http.MethodDelete, "/api/v1/users/primary-super-admin", "")
	if protected.Code != http.StatusBadRequest {
		t.Fatalf("删除主超级管理员应被拒绝：%d %s", protected.Code, protected.Body.String())
	}
}

func TestUserManagementRejectsRequestsBeforeSetup(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = false
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})

	response := serveUserRequest(server, nil, http.MethodGet, "/api/v1/users", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("users API should return 409 before setup: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "\u8bf7\u5148\u5b8c\u6210\u521d\u59cb\u5316\u914d\u7f6e") {
		t.Fatalf("unexpected setup error: %s", response.Body.String())
	}
}

func serveUserRequest(server *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func httpUserSessionCookie(server *Server, userID, role string) *http.Cookie {
	token := userID + "-session"
	now := server.now()
	hash, err := bcrypt.GenerateFromPassword([]byte("FixturePass123"), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	user := repository.User{
		ID:           userID,
		Username:     userID,
		Email:        userID + "@example.test",
		Role:         role,
		Status:       repository.UserStatusActive,
		PasswordHash: string(hash),
	}
	if userID == "primary-super-admin" {
		user.Username = server.config.Admin.Username
		user.Email = server.config.Admin.Email
		user.PasswordHash = server.config.Admin.PasswordHash
	} else {
		if server.users == nil {
			server.users = &sessionFixtureUsers{items: make(map[string]repository.User)}
		}
		if stored, err := server.users.GetUserByID(context.Background(), userID); err == nil {
			user = stored
		} else if err := server.users.CreateUser(context.Background(), user); err != nil {
			panic(err)
		}
	}
	server.authMu.Lock()
	server.authSessions[token] = authSession{
		UserID: user.ID, Username: user.Username, Email: user.Email, Role: user.Role, PasswordHash: user.PasswordHash,
		ExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour),
	}
	server.authMu.Unlock()
	return &http.Cookie{Name: authCookieName, Value: token, Path: "/"}
}

type sessionFixtureUsers struct {
	repository.UserRepository
	items map[string]repository.User
}

func (r *sessionFixtureUsers) GetUserByID(_ context.Context, id string) (repository.User, error) {
	if user, ok := r.items[id]; ok {
		return user, nil
	}
	return repository.User{}, sql.ErrNoRows
}

func (r *sessionFixtureUsers) CreateUser(_ context.Context, user repository.User) error {
	r.items[user.ID] = user
	return nil
}

func (r *sessionFixtureUsers) EnsureSuperAdmin(_ context.Context, user repository.User) error {
	user.Role = repository.UserRoleSuperAdmin
	user.Status = repository.UserStatusActive
	r.items[user.ID] = user
	return nil
}

func httpUserPasswordHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成用户测试密码摘要失败：%v", err)
	}
	return string(hash)
}

var _ = context.Background
