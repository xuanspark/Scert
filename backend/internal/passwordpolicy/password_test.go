package passwordpolicy

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		password string
		username string
		wantErr  bool
	}{
		{name: "合法密码", password: "Password123", username: "admin"},
		{name: "length at max", password: "Pass12345678", username: "admin"},
		{name: "length above max", password: "Pass123456789", username: "admin", wantErr: true},
		{name: "支持中文字符", password: "安全密码2026", username: "admin"},
		{name: "长度不足", password: "Pass123", username: "admin", wantErr: true},
		{name: "缺少数字", password: "PasswordOnly", username: "admin", wantErr: true},
		{name: "缺少字母", password: "123456789", username: "admin", wantErr: true},
		{name: "等于用户名", password: "Admin123", username: "Admin123", wantErr: true},
		{name: "包含控制字符", password: "Password1\n", username: "admin", wantErr: true},
		{name: "长度超限", password: "Password1" + strings.Repeat("a", MaxLength), username: "admin", wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.password, test.username)
			if (err != nil) != test.wantErr {
				t.Fatalf("密码校验结果不正确：err=%v", err)
			}
		})
	}
}
