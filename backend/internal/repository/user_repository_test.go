package repository

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
)

func TestSQLUserRepositoryCRUDAndLookup(t *testing.T) {
	db := openUserTestDatabase(t)
	defer db.Close()
	repo := NewSQLUserRepository(db.DB, "sqlite")
	hash := userTestPasswordHash(t, "UserPass123")
	item := User{ID: "user-1", Username: "Operator", Email: "Operator@Example.Test", PasswordHash: hash, Role: UserRoleUser, Status: UserStatusActive}
	if err := repo.CreateUser(t.Context(), item); err != nil {
		t.Fatalf("创建用户失败：%v", err)
	}

	byName, err := repo.FindByIdentifier(t.Context(), " operator ")
	if err != nil || byName.ID != item.ID || byName.Username != "operator" {
		t.Fatalf("按用户名查询用户失败：%v %+v", err, byName)
	}
	byEmail, err := repo.FindByIdentifier(t.Context(), "OPERATOR@EXAMPLE.TEST")
	if err != nil || byEmail.ID != item.ID || byEmail.Email != "operator@example.test" {
		t.Fatalf("按邮箱查询用户失败：%v %+v", err, byEmail)
	}

	encoded, err := json.Marshal(byName)
	if err != nil || strings.Contains(string(encoded), hash) || strings.Contains(string(encoded), "password_hash") {
		t.Fatalf("用户 JSON 不应包含密码摘要：%s", encoded)
	}

	updated := byName
	updated.Role = UserRoleAdmin
	updated.Status = UserStatusDisabled
	if err := repo.UpdateUser(t.Context(), updated); err != nil {
		t.Fatalf("更新用户失败：%v", err)
	}
	loaded, err := repo.GetUserByID(t.Context(), item.ID)
	if err != nil || loaded.Role != UserRoleAdmin || loaded.Status != UserStatusDisabled {
		t.Fatalf("读取更新后的用户失败：%v %+v", err, loaded)
	}
	if bcrypt.CompareHashAndPassword([]byte(loaded.PasswordHash), []byte("UserPass123")) != nil {
		t.Fatal("更新用户不应改变原密码")
	}

	items, err := repo.ListUsers(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("列出用户失败：%v %+v", err, items)
	}
	if err := repo.DeleteUser(t.Context(), item.ID); err != nil {
		t.Fatalf("删除用户失败：%v", err)
	}
	if _, err := repo.GetUserByID(t.Context(), item.ID); !errors.Is(err, context.Canceled) && !IsNotFound(err) {
		t.Fatalf("删除用户后查询结果错误：%v", err)
	}
}

func TestSQLUserRepositoryEnforcesIdentityAndSuperAdminRules(t *testing.T) {
	db := openUserTestDatabase(t)
	defer db.Close()
	repo := NewSQLUserRepository(db.DB, "sqlite")
	first := User{ID: "user-1", Username: "operator", Email: "operator@example.test", PasswordHash: userTestPasswordHash(t, "UserPass123"), Role: UserRoleUser, Status: UserStatusActive}
	if err := repo.CreateUser(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	duplicateName := first
	duplicateName.ID = "user-2"
	duplicateName.Email = "second@example.test"
	if err := repo.CreateUser(t.Context(), duplicateName); !IsConflict(err) {
		t.Fatalf("重复用户名未被拒绝：%v", err)
	}
	duplicateEmail := first
	duplicateEmail.ID = "user-3"
	duplicateEmail.Username = "second"
	if err := repo.CreateUser(t.Context(), duplicateEmail); !IsConflict(err) {
		t.Fatalf("重复邮箱未被拒绝：%v", err)
	}

	if err := repo.EnsureSuperAdmin(t.Context(), User{ID: "primary-super-admin", Username: "admin", Email: "admin@example.test", PasswordHash: userTestPasswordHash(t, "AdminPass123")}); err != nil {
		t.Fatalf("保存超级管理员失败：%v", err)
	}
	if err := repo.DeleteUser(t.Context(), "primary-super-admin"); !errors.Is(err, ErrSuperAdminProtected) {
		t.Fatalf("主超级管理员不应被删除：%v", err)
	}
	secondAdmin := User{ID: "super-2", Username: "owner", PasswordHash: userTestPasswordHash(t, "OwnerPass123"), Role: UserRoleSuperAdmin, Status: UserStatusActive}
	if err := repo.CreateUser(t.Context(), secondAdmin); err != nil {
		t.Fatalf("创建第二个超级管理员失败：%v", err)
	}
	if err := repo.DeleteUser(t.Context(), secondAdmin.ID); err != nil {
		t.Fatalf("删除第二个超级管理员失败：%v", err)
	}
}

func TestSQLUserRepositoryNilDatabase(t *testing.T) {
	var repo *SQLUserRepository
	if err := repo.CreateUser(context.Background(), User{}); err == nil {
		t.Fatal("空数据库连接应返回错误")
	}
}

func openUserTestDatabase(t *testing.T) *database.Database {
	t.Helper()
	directory := t.TempDir()
	cfg := config.Default(directory).Database
	cfg.DSN = filepath.Join(directory, "data", "users.db")
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("打开用户测试数据库失败：%v", err)
	}
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		_ = db.Close()
		t.Fatalf("迁移用户测试数据库失败：%v", err)
	}
	return db
}

func userTestPasswordHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	return string(hash)
}
