package bootstrap

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func completionNotificationInput() *NotificationInput {
	return &NotificationInput{
		HTTPEnabled: true, Endpoint: "https://mail.example.test", HTTPAuthMode: "session",
		HTTPCredentials: map[string]string{"account": "mail-owner", "password": "private-http-password"},
		SMTPEnabled:     true, SMTPHost: "smtp.example.test", SMTPUsername: "smtp-owner", SMTPPassword: "private-smtp-password",
		IncludeManagementURL: true, IncludeCertificateSerial: true,
		Recipients: []string{"notify@example.test"}, TimeoutSecs: 15, RetryCount: 0, RetryIntervalsSecs: []int{},
	}
}

func TestCompleterPreservesExistingNotificationWhenOmitted(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	fixture.base.Notification.HTTPEnabled = true
	fixture.base.Notification.Endpoint = "https://mail.example.test"
	fixture.base.Notification.HTTPAuthMode = "bearer"
	fixture.base.Notification.HTTPCredentialRef = "secret://notification/existing"
	fixture.base.Notification.SenderEmail = "sender@example.test"
	fixture.base.Notification.Recipients = []string{"notify@example.test"}
	fixture.base.Notification.HTTPHeaders = map[string]string{"X-Client": "Scert"}
	fixture.base.Notification.IncludeDeploymentDetails = true
	if _, err := fixture.completer(nil).Complete(t.Context(), fixture.request("admin.example.test")); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Notification, fixture.base.Notification) {
		t.Fatalf("省略通知表单时原有通知设置发生丢失：%+v", stored.Notification)
	}
}

func TestCompleterStoresNotificationCredentialsWithoutPlaintext(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	request := fixture.request("admin.example.test")
	request.Notification = completionNotificationInput()
	if _, err := fixture.completer(nil).Complete(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Notification.HTTPEnabled || !stored.Notification.SMTPEnabled || stored.Notification.RetryCount != 0 ||
		len(stored.Notification.RetryIntervalsSecs) != 0 || !stored.Notification.IncludeCertificateSerial {
		t.Fatalf("通知选项未完整保存：%+v", stored.Notification)
	}
	httpCredentials, err := fixture.store.Get(t.Context(), stored.Notification.HTTPCredentialRef)
	if err != nil || httpCredentials["password"] != "private-http-password" {
		t.Fatalf("HTTP 通知凭据未进入敏感存储：%v", err)
	}
	smtpCredentials, err := fixture.store.Get(t.Context(), stored.Notification.SMTPCredentialRef)
	if err != nil || smtpCredentials["password"] != "private-smtp-password" {
		t.Fatalf("SMTP 通知凭据未进入敏感存储：%v", err)
	}
	data, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-http-password", "private-smtp-password"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("正式配置包含通知明文密码")
		}
	}
}

func TestCompleterReclaimsOnlyNewNotificationCredentialsOnFailure(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	existing := "secret://notification/existing"
	if err := fixture.store.Put(t.Context(), existing, map[string]string{"token": "existing-token"}); err != nil {
		t.Fatal(err)
	}
	request := fixture.request("admin.example.test")
	request.Notification = completionNotificationInput()
	_, err := fixture.completer(func(dependencies *CompleteDependencies) {
		dependencies.SaveConfig = func(config.Config, string) error { return errors.New("save failed") }
	}).Complete(t.Context(), request)
	if err == nil {
		t.Fatal("保存失败未返回错误")
	}
	fixture.store.mu.Lock()
	defer fixture.store.mu.Unlock()
	if len(fixture.store.values) != 1 || fixture.store.values[existing]["token"] != "existing-token" {
		t.Fatalf("回滚必须保留已有凭据并回收新凭据：剩余=%d", len(fixture.store.values))
	}
}

type failingNotificationStore struct {
	*completionMemorySecrets
	puts int
}

func (store *failingNotificationStore) Put(ctx context.Context, reference string, values map[string]string) error {
	store.puts++
	if err := store.completionMemorySecrets.Put(ctx, reference, values); err != nil {
		return err
	}
	if store.puts == 2 {
		return errors.New("partial write failure")
	}
	return nil
}

func TestNotificationPartialWriteFailureReclaimsAllCreatedReferences(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	store := &failingNotificationStore{completionMemorySecrets: fixture.store}
	request := fixture.request("admin.example.test")
	request.Notification = completionNotificationInput()
	_, err := fixture.completer(func(dependencies *CompleteDependencies) {
		dependencies.Credentials = store
	}).Complete(t.Context(), request)
	if err == nil || store.puts != 2 || fixture.issuer.calls != 0 {
		t.Fatalf("部分写入失败未中止初始化：%v", err)
	}
	fixture.store.mu.Lock()
	defer fixture.store.mu.Unlock()
	if len(fixture.store.values) != 0 {
		t.Fatal("部分写入失败遗留通知或 DNS 临时凭据")
	}
}

func TestNotificationAuthenticationModes(t *testing.T) {
	for _, item := range []struct {
		mode       string
		input      map[string]string
		key, value string
	}{
		{"bearer", map[string]string{"access_token": "test-token"}, "token", "test-token"},
		{"hmac_sha256", map[string]string{"hmac_secret": "test-key"}, "secret", "test-key"},
		{"session", map[string]string{"username": "owner", "password": "test-password"}, "account", "owner"},
	} {
		t.Run(item.mode, func(t *testing.T) {
			values, err := notificationCredentials(item.mode, item.input)
			if err != nil || values[item.key] != item.value {
				t.Fatalf("通知凭据兼容字段转换不正确：%v", err)
			}
			if _, err := notificationCredentials(item.mode, nil); err == nil {
				t.Fatal("缺少通知认证凭据未拒绝")
			}
		})
	}
}

func TestNotificationRejectsInvalidSettingsBeforeWritingSecrets(t *testing.T) {
	for _, edit := range []func(*NotificationInput){
		func(input *NotificationInput) { input.Endpoint = "" },
		func(input *NotificationInput) { input.RetryCount = -1 },
		func(input *NotificationInput) { input.SMTPPassword = "" },
		func(input *NotificationInput) { input.SMTPMode = "plain" },
		func(input *NotificationInput) { input.HTTPCredentials = nil },
	} {
		fixture := newCompletionFixture(t, "admin.example.test")
		input := completionNotificationInput()
		edit(input)
		_, cleanup, err := fixture.completer(nil).prepareNotification(t.Context(), fixture.base.Notification, input)
		if err == nil || cleanup != nil {
			t.Fatal("无效通知配置不得写入敏感存储")
		}
	}
}

func TestManagementURLHandlesDirectAndProxyEntrypoints(t *testing.T) {
	for _, item := range []struct {
		hostname, ip, mode string
		port               int
		want               string
	}{
		{"admin.example.test", "", "direct", 443, "https://admin.example.test/"},
		{"admin.example.test", "", "direct", 8443, "https://admin.example.test:8443/"},
		{"admin.example.test", "", "proxy", 8080, "https://admin.example.test/"},
		{"", "203.0.113.70", "direct", 8443, "https://203.0.113.70:8443/"},
		{"", "2001:db8::70", "direct", 443, "https://[2001:db8::70]/"},
		{"", "2001:db8::70", "direct", 8443, "https://[2001:db8::70]:8443/"},
		{"", "2001:db8::70", "proxy", 8080, "https://[2001:db8::70]/"},
	} {
		candidate := config.Config{
			Web:    config.WebConfig{Hostname: item.hostname, PublicIP: item.ip},
			Server: config.ServerConfig{Port: item.port, HTTPSMode: item.mode},
		}
		if actual := managementURL(candidate); actual != item.want {
			t.Fatalf("管理入口不正确：实际=%s，预期=%s", actual, item.want)
		}
	}
}
