package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSecurityCodeHTTPRecipientIsolation(t *testing.T) {
	received := make(chan mailPayload, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload mailPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	cfg := newTestConfig(endpoint.URL)
	cfg.Recipients = []string{"unrelated@example.test"}
	service := New(cfg, nil)
	if err := service.SendSecurityCode(t.Context(), "bound@example.test", "01234567"); err != nil {
		t.Fatal(err)
	}
	payload := <-received
	if len(payload.To) != 1 || payload.To[0].Email != "bound@example.test" || len(payload.CC) != 0 || len(payload.BCC) != 0 {
		t.Fatal("验证码只能发送到绑定邮箱")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "unrelated@example.test") || !strings.Contains(payload.Text, "01234567") || !strings.Contains(payload.HTML, "01234567") {
		t.Fatal("验证码内容或收件人隔离错误")
	}
}

func TestSecurityCodeRejectsInvalidInputWithoutSending(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	for _, tc := range []struct{ name, recipient, code string }{
		{"short", "bound@example.test", "1234567"},
		{"long", "bound@example.test", "123456789"},
		{"letters", "bound@example.test", "1234567a"},
		{"space", "bound@example.test", "1234567 "},
		{"empty", "", "12345678"},
		{"multiple", "a@example.test,b@example.test", "12345678"},
		{"display-name", "Admin <a@example.test>", "12345678"},
		{"header-injection", "a@example.test\r\nBcc: b@example.test", "12345678"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := New(newTestConfig(endpoint.URL), nil).SendSecurityCode(t.Context(), tc.recipient, tc.code); err == nil {
				t.Fatal("无效验证码或收件人被接受")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("校验失败仍然发送了邮件")
	}
}

func TestSecurityCodeRequiresConfiguredChannel(t *testing.T) {
	cfg := newTestConfig("")
	cfg.HTTPEnabled = false
	for _, service := range []*Service{nil, New(cfg, nil)} {
		if err := service.SendSecurityCode(t.Context(), "bound@example.test", "12345678"); err == nil {
			t.Fatal("未配置邮件通道不能报告成功")
		}
	}
}

func TestSecurityCodeCancellationAndFailureDoNotLeak(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("12345678 sensitive-upstream-detail"))
	}))
	defer endpoint.Close()
	service := New(newTestConfig(endpoint.URL), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.SendSecurityCode(ctx, "bound@example.test", "12345678"); !errors.Is(err, context.Canceled) {
		t.Fatal("已取消的发送必须立即失败")
	}
	if calls.Load() != 0 {
		t.Fatal("取消后仍发起网络请求")
	}
	err := service.SendSecurityCode(t.Context(), "bound@example.test", "12345678")
	if err == nil || strings.Contains(err.Error(), "12345678") || strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatal("发送失败泄露了验证码或上游响应")
	}
}

func TestSecurityCodeSMTPAndHTTPFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "smtp-only"
		if fallback {
			name = "http-fallback"
		}
		t.Run(name, func(t *testing.T) {
			smtp := newSMTPTestServer(t, true, false)
			host, port := smtp.address()
			cfg := smtpTestConfig(host, port, "implicit_tls")
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer endpoint.Close()
			cfg.HTTPEnabled = fallback
			cfg.Endpoint = endpoint.URL
			cfg.HTTPPath = "/api/email/send"
			cfg.HTTPAuthMode = "none"
			service := New(cfg, credentialReaderStub{values: map[string]string{"username": "user", "password": "test-password"}})
			service.smtpTLSConfig = smtp.clientTLSConfig
			if err := service.SendSecurityCode(t.Context(), "bound@example.test", "12345678"); err != nil {
				t.Fatal(err)
			}
			smtp.mu.Lock()
			defer smtp.mu.Unlock()
			if len(smtp.messages) != 1 {
				t.Fatal("验证码邮件发送数量错误")
			}
			message := string(smtp.messages[0])
			if !strings.Contains(message, "bound@example.test") || strings.Contains(message, "receiver@example.com") {
				t.Fatal("备用通道未保持绑定邮箱隔离")
			}
			if fallback && calls.Load() != 1 {
				t.Fatal("未先尝试配置的 HTTP 通道")
			}
		})
	}
}

func TestSecurityCodeSmailSystemAccountRequiresSMTP(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	cfg := newTestConfig(endpoint.URL)
	cfg.SMTPUsername = "notify:scert@example.test"
	cfg.SMTPEnabled = false
	service := New(cfg, nil)
	if err := service.SendSecurityCode(t.Context(), "bound@example.test", "12345678"); err == nil || !strings.Contains(err.Error(), "必须启用 SMTP") {
		t.Fatalf("Smail 系统通知未拒绝 HTTP-only 配置：%v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("Smail 系统通知账号禁止调用 HTTP 通道")
	}
}
