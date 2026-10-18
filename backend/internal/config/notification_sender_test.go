package config

import "testing"

func TestNotificationSenderIdentity(t *testing.T) {
	for _, item := range []struct {
		name, username, senderName, senderEmail, wantName, wantEmail string
	}{
		{"system_default_name", "notify:scert@example.test", "", "", "系统通知", "scert@example.test"},
		{"system_custom_name", "notify:scert@example.test", "Scert通知", "", "Scert通知", "scert@example.test"},
		{"system_explicit_sender", "notify:scert@example.test", "Scert", "custom@example.test", "Scert", "custom@example.test"},
		{"normalized", " NOTIFY:SCERT@EXAMPLE.TEST ", "", "", "系统通知", "scert@example.test"},
		{"smtp_compatibility", "sender@example.test", "Scert", "", "Scert", "sender@example.test"},
		{"explicit_legacy", "smtp-login", "Scert", "sender@example.test", "Scert", "sender@example.test"},
	} {
		t.Run(item.name, func(t *testing.T) {
			cfg := NotificationConfig{SMTPEnabled: true, SMTPUsername: item.username, SenderName: item.senderName, SenderEmail: item.senderEmail}
			name, email, err := cfg.SenderIdentity()
			if err != nil || name != item.wantName || email != item.wantEmail {
				t.Fatalf("发件身份不匹配：name=%q email=%q err=%v", name, email, err)
			}
		})
	}
}

func TestNotificationSenderRejectsInvalidSystemAccount(t *testing.T) {
	for _, username := range []string{"notify:", "notify:user", "notify:@example.test", "notify:a@b@example.test", "notify:User <a@example.test>", "notify:a@example.test\r\nBcc:other@example.test", "smtp-login", ""} {
		cfg := NotificationConfig{SMTPEnabled: true, SMTPUsername: username}
		if _, _, err := cfg.SenderIdentity(); err == nil {
			t.Fatal("无效账号不得回退到管理员邮箱")
		}
	}
}

func TestNotificationSenderPreservesHTTPIdentity(t *testing.T) {
	cfg := NotificationConfig{SMTPUsername: "notify:unused@example.test", SenderName: "兼容通知", SenderEmail: "sender@example.test"}
	name, email, err := cfg.SenderIdentity()
	if err != nil || name != cfg.SenderName || email != cfg.SenderEmail {
		t.Fatal("未启用 SMTP 时不得改写原有 HTTP 发件人")
	}
}
