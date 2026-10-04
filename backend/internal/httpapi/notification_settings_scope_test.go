package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type notificationScopeSecrets struct {
	fakeSecretReaderStore
	puts    int
	deletes int
}

func (s *notificationScopeSecrets) Put(ctx context.Context, ref string, value map[string]string) error {
	s.puts++
	return s.fakeSecretReaderStore.Put(ctx, ref, value)
}

func (s *notificationScopeSecrets) Delete(ctx context.Context, ref string) error {
	s.deletes++
	return s.fakeSecretReaderStore.Delete(ctx, ref)
}

const notificationScopeBody = `{"notification":{"smtp_enabled":true,"smtp_host":"smtp.example.test","smtp_port":587,"smtp_mode":"starttls","smtp_username":"notify:system@example.test","smtp_password":"fixture-notification-password","recipients":["recipient@example.test"]}}`

func TestNotificationSettingsOnlySuperAdminCanConfigure(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	f.server.config.Web.Hostname = "scert.example.test"
	f.server.config.DNS.VerificationState = "verified"
	f.server.configPath = filepath.Join(t.TempDir(), "scert.json")
	if err := f.server.config.Save(f.server.configPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	previous := f.server.config.Notification
	secrets := &notificationScopeSecrets{}
	f.server.secrets = secrets
	for _, actor := range []string{"", "global-admin", "manager", "viewer", "target"} {
		t.Run(actor, func(t *testing.T) {
			read := serveUserRequest(f.server, f.cookies[actor], http.MethodGet, "/api/v1/settings", "")
			response := serveUserRequest(f.server, f.cookies[actor], http.MethodPut, "/api/v1/settings", notificationScopeBody)
			want := http.StatusForbidden
			if actor == "" {
				want = http.StatusUnauthorized
			}
			if read.Code != want || read.Header().Get("Cache-Control") != "private, no-store" || read.Header().Get("Vary") != "Cookie" {
				t.Fatalf("通知配置读权限或缓存隔离错误：%s，状态 %d", actor, read.Code)
			}
			if response.Code != want || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
				t.Fatalf("通知配置写权限或缓存隔离错误：%s，状态 %d", actor, response.Code)
			}
			after, err := os.ReadFile(f.server.configPath)
			if err != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(previous, f.server.config.Notification) {
				t.Fatal("被拒绝的请求修改了通知配置", err)
			}
			if secrets.puts != 0 || secrets.deletes != 0 || secrets.getCalls != 0 {
				t.Fatal("被拒绝的请求仍访问了通知秘密仓库")
			}
			if strings.Contains(response.Body.String(), "fixture-notification-password") {
				t.Fatal("拒绝响应回显了提交的密码")
			}
		})
	}
	response := serveUserRequest(f.server, f.cookies["primary-super-admin"], http.MethodPut, "/api/v1/settings", notificationScopeBody)
	if response.Code != http.StatusOK {
		t.Fatalf("超级管理员无法保存通知配置：%d %s", response.Code, response.Body.String())
	}
	saved, err := config.Load(f.server.configPath)
	if err != nil || !saved.Notification.SMTPEnabled || saved.Notification.SMTPHost != "smtp.example.test" || saved.Notification.SMTPUsername != "notify:system@example.test" {
		t.Fatal("超级管理员通知配置未正确持久化", err)
	}
	if secrets.puts != 1 || saved.Notification.SMTPCredentialRef == "" {
		t.Fatal("通知密码没有独立保存到秘密仓库")
	}
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		if method == http.MethodGet {
			response = serveUserRequest(f.server, f.cookies["primary-super-admin"], method, "/api/v1/settings", "")
		}
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "fixture-notification-password") || strings.Contains(response.Body.String(), "secret://") {
			t.Fatal("超级管理员设置响应不应包含密码或秘密引用")
		}
	}
}

func TestNotificationSettingsRejectsRevokedSuperAdminSession(t *testing.T) {
	for _, change := range []string{"demoted", "disabled", "deleted"} {
		t.Run(change, func(t *testing.T) {
			f := newProjectMemberAPIFixture(t)
			cookie := httpUserSessionCookie(f.server, "notification-owner", repository.UserRoleSuperAdmin)
			response := serveUserRequest(f.server, cookie, http.MethodGet, "/api/v1/settings", "")
			if response.Code != http.StatusOK {
				t.Fatal("初始超级管理员会话不可用")
			}
			statement := "UPDATE users SET role = 'admin' WHERE id = ?"
			if change == "disabled" {
				statement = "UPDATE users SET status = 'disabled' WHERE id = ?"
			} else if change == "deleted" {
				statement = "DELETE FROM users WHERE id = ?"
			}
			if _, err := f.db.ExecContext(t.Context(), statement, "notification-owner"); err != nil {
				t.Fatal(err)
			}
			secrets := &notificationScopeSecrets{}
			f.server.secrets = secrets
			previous := f.server.config.Notification
			for _, method := range []string{http.MethodPut, http.MethodGet} {
				response = serveUserRequest(f.server, cookie, method, "/api/v1/settings", notificationScopeBody)
				if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatalf("权限失效后旧会话仍可操作通知配置：%s %d", method, response.Code)
				}
			}
			if !reflect.DeepEqual(previous, f.server.config.Notification) || secrets.puts != 0 || secrets.deletes != 0 || secrets.getCalls != 0 {
				t.Fatal("失效会话仍修改了配置或访问秘密仓库")
			}
		})
	}
}
