package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func protectedUserServer(t *testing.T) (*Server, *repository.SQLUserRepository, string) {
	t.Helper()
	directory := t.TempDir()
	cfg := config.Default(directory)
	cfg.SetupReady = true
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "scert.example.test"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	cfg.Database.DSN = filepath.Join(directory, "data", "users.db")
	configPath := filepath.Join(directory, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email,
		PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatal(err)
	}
	return New(Dependencies{Config: cfg, ConfigPath: configPath, Repository: &stubDomainRepository{}, Users: users}), users, configPath
}

func TestUserManagementProtectsPrimaryAccount(t *testing.T) {
	for _, change := range []struct{ name, field, value string }{
		{"username", "username", "renamed"},
		{"email", "email", "changed@example.test"},
		{"password", "password", "Changed123"},
		{"status", "status", "disabled"},
		{"role", "role", "admin"},
	} {
		t.Run(change.name, func(t *testing.T) {
			server, users, _ := protectedUserServer(t)
			before, err := users.GetUserByID(t.Context(), "primary-super-admin")
			if err != nil {
				t.Fatal(err)
			}
			cookie := loginAuthTest(t, server, "admin", "AdminPass123", false).Result().Cookies()[0]
			payload := map[string]string{"username": before.Username, "email": before.Email, "role": before.Role, "status": before.Status}
			payload[change.field] = change.value
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			response := serveUserRequest(server, cookie, http.MethodPut, "/api/v1/users/primary-super-admin", string(body))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("用户管理不应修改主超级管理员：%d %s", response.Code, response.Body.String())
			}
			after, err := users.GetUserByID(t.Context(), before.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("被拒绝的修改不应改变数据库：%v", err)
			}
			if response := authSessionRequest(server, cookie); response.Code != http.StatusOK {
				t.Fatalf("被拒绝的修改不应注销现有会话：%d", response.Code)
			}
			loginAuthTest(t, server, "admin@example.test", "AdminPass123", false)
		})
	}
}

func TestUserManagementRejectsSuperAdminCreationAndPromotion(t *testing.T) {
	server, users, _ := protectedUserServer(t)
	cookie := loginAuthTest(t, server, "admin", "AdminPass123", false).Result().Cookies()[0]
	for _, role := range []string{"super_admin", " SUPER_ADMIN "} {
		body, _ := json.Marshal(userRequest{Username: "owner", Password: "OwnerPass123", Role: role})
		response := serveUserRequest(server, cookie, http.MethodPost, "/api/v1/users", string(body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("不应新增超级管理员：%d %s", response.Code, response.Body.String())
		}
	}
	created := serveUserRequest(server, cookie, http.MethodPost, "/api/v1/users", `{"username":"operator","password":"OperPass123","role":"admin"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("应允许新增管理员：%d %s", created.Code, created.Body.String())
	}
	var item repository.User
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"super_admin", " SUPER_ADMIN "} {
		body, _ := json.Marshal(userRequest{Username: item.Username, Role: role})
		response := serveUserRequest(server, cookie, http.MethodPut, "/api/v1/users/"+item.ID, string(body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("不应提权为超级管理员：%d %s", response.Code, response.Body.String())
		}
	}
	stored, err := users.GetUserByID(t.Context(), item.ID)
	if err != nil || stored.Role != repository.UserRoleAdmin {
		t.Fatalf("拒绝提权后应保留原角色：%v", err)
	}
	items, err := users.ListUsers(t.Context())
	if err != nil || len(items) != 2 {
		t.Fatalf("拒绝新增后不应产生额外用户：%v", err)
	}
}

func TestPrimaryPasswordChangePersistsWithDatabase(t *testing.T) {
	server, users, configPath := protectedUserServer(t)
	cookie := loginAuthTest(t, server, "admin", "AdminPass123", false).Result().Cookies()[0]
	response := performChangePasswordRequest(t, server, cookie, `{"current_password":"AdminPass123","new_password":"Changed123","confirm_password":"Changed123"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("持当前密码的自助改密应成功：%d %s", response.Code, response.Body.String())
	}
	if response := authSessionRequest(server, cookie); response.Code != http.StatusUnauthorized {
		t.Fatalf("改密后旧会话应失效：%d", response.Code)
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := users.GetUserByID(t.Context(), "primary-super-admin")
	if err != nil || stored.PasswordHash != saved.Admin.PasswordHash {
		t.Fatalf("改密后配置和数据库应一致：%v", err)
	}
	restarted := New(Dependencies{Config: saved, ConfigPath: configPath, Repository: &stubDomainRepository{}, Users: users})
	for _, candidate := range []*Server{server, restarted} {
		if old := performLoginRequest(t, candidate, "admin", "AdminPass123"); old.Code != http.StatusUnauthorized {
			t.Fatalf("旧密码不应登录成功：%d", old.Code)
		}
		login := loginAuthTest(t, candidate, "admin@example.test", "Changed123", false)
		if session := authSessionRequest(candidate, login.Result().Cookies()[0]); session.Code != http.StatusOK {
			t.Fatalf("新密码登录后会话应有效：%d", session.Code)
		}
	}
}

func TestPrimaryLoginRejectsStaleDatabaseCredentials(t *testing.T) {
	server, users, _ := protectedUserServer(t)
	stored, err := users.GetUserByID(t.Context(), "primary-super-admin")
	if err != nil {
		t.Fatal(err)
	}
	stored.PasswordHash = httpUserPasswordHash(t, "StalePass123")
	if err := users.UpdateUser(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []string{stored.Username, stored.Email} {
		response := performLoginRequest(t, server, identifier, "StalePass123")
		if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
			t.Fatalf("数据库残留凭据不应取得登录会话：%d", response.Code)
		}
	}
	login := loginAuthTest(t, server, "admin", "AdminPass123", false)
	if session := authSessionRequest(server, login.Result().Cookies()[0]); session.Code != http.StatusOK {
		t.Fatalf("配置中的主账号凭据应仍然有效：%d", session.Code)
	}
}
