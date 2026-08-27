package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestNormalizeIdentifier(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
		shouldErr bool
	}{
		{name: "username", input: " Admin ", expected: "admin"},
		{name: "email", input: " Admin@Example.COM ", expected: "admin@example.com"},
		{name: "display name rejected", input: "Admin <admin@example.com>", shouldErr: true},
		{name: "line break rejected", input: "admin\n@example.com", shouldErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := NormalizeIdentifier(test.input)
			if test.shouldErr {
				if err == nil {
					t.Fatal("应拒绝无效登录标识")
				}
				return
			}
			if err != nil || actual != test.expected {
				t.Fatalf("登录标识归一化结果错误：%q，错误：%v", actual, err)
			}
		})
	}
}

func TestAuthenticateAdminByUsernameOrEmail(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("Password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	for _, identifier := range []string{"Admin", "admin@example.com"} {
		account, err := AuthenticateAdmin(identifier, "Password123", "admin", "admin@example.com", string(hash))
		if err != nil {
			t.Fatalf("使用 %s 登录失败：%v", identifier, err)
		}
		if account.Role != "super_admin" {
			t.Fatalf("登录权限错误：%s", account.Role)
		}
	}
	if _, err := AuthenticateAdmin("other@example.com", "Password123", "admin", "admin@example.com", string(hash)); !IsInvalidCredentials(err) {
		t.Fatalf("未匹配账号时应返回统一认证错误：%v", err)
	}
}
