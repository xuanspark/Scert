package notification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type credentialReaderStub struct {
	values map[string]string
}

func (s credentialReaderStub) Get(context.Context, string) (map[string]string, error) {
	return s.values, nil
}

func newTestConfig(endpoint string) config.NotificationConfig {
	return config.NotificationConfig{
		HTTPEnabled:        true,
		Endpoint:           endpoint,
		HTTPPath:           "/api/email/send",
		HTTPLoginPath:      "/api/login",
		HTTPAuthMode:       "none",
		EventMode:          "detailed",
		SenderName:         "Scert",
		SenderEmail:        "admin@example.com",
		Recipients:         []string{"receiver@example.com"},
		TimeoutSecs:        2,
		RetryCount:         0,
		RetryIntervalsSecs: []int{},
	}
}

func TestSendBuildsSmailPayload(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/email/send" {
			t.Fatalf("请求方法或路径错误：%s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("通知请求编码声明错误：%q", request.Header.Get("Content-Type"))
		}
		if request.Header.Get("X-Scert-Request-Id") == "" || request.Header.Get("X-Scert-Timestamp") == "" {
			t.Fatal("缺少通知安全请求头")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "证书续期") {
			t.Fatalf("通知请求未使用 UTF-8 编码：%q", string(body))
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	service := New(newTestConfig(server.URL), nil)
	service.requestID = func() string { return "request-id" }
	service.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	when := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	err := service.Send(context.Background(), Event{
		Type:          "certificate_renewal",
		Status:        "success",
		Domain:        "example.com",
		Domains:       []string{"example.com", "www.example.com"},
		ExpiresAt:     &when,
		ManagementURL: "https://scert.example.com",
	})
	if err != nil {
		t.Fatalf("发送通知失败：%v", err)
	}
	if payload["subject"] == "" || !strings.Contains(payload["text"].(string), "证书续期") {
		t.Fatalf("通知正文未正确生成：%v", payload)
	}
	to := payload["to"].([]any)
	if len(to) != 1 || to[0].(map[string]any)["email"] != "receiver@example.com" {
		t.Fatalf("收件人未正确生成：%v", payload["to"])
	}
}

func TestSendSupportsBearerAndRetries(t *testing.T) {
	attempts := 0
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		authorization = request.Header.Get("Authorization")
		if attempts < 3 {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "bearer"
	cfg.HTTPCredentialRef = "secret://mail-token"
	cfg.RetryCount = 2
	cfg.RetryIntervalsSecs = []int{1, 1}
	service := New(cfg, credentialReaderStub{values: map[string]string{"token": "test-token"}})
	service.sleep = func(context.Context, time.Duration) error { return nil }
	if err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("重试后发送通知失败：%v", err)
	}
	if attempts != 3 || authorization != "Bearer test-token" {
		t.Fatalf("Bearer 或重试次数错误：attempts=%d authorization=%q", attempts, authorization)
	}
}

func TestSendSupportsHMACAndProtectsHeaders(t *testing.T) {
	var receivedSignature string
	var receivedTimestamp string
	var receivedRequestID string
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedSignature = request.Header.Get("X-Scert-Signature")
		receivedTimestamp = request.Header.Get("X-Scert-Timestamp")
		receivedRequestID = request.Header.Get("X-Scert-Request-Id")
		receivedBody, _ = io.ReadAll(request.Body)
		writer.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "hmac_sha256"
	cfg.HTTPCredentialRef = "secret://mail-hmac"
	service := New(cfg, credentialReaderStub{values: map[string]string{"secret": "hmac-secret"}})
	service.requestID = func() string { return "fixed-request" }
	service.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	if err := service.Send(context.Background(), Event{Type: "certificate_import", Status: "unknown", Domain: "example.com"}); err != nil {
		t.Fatalf("HMAC 通知发送失败：%v", err)
	}
	expected := "sha256=" + sign("hmac-secret", receivedTimestamp, receivedRequestID, receivedBody)
	if receivedSignature != expected {
		t.Fatalf("HMAC 签名错误：got=%q want=%q", receivedSignature, expected)
	}

	cfg.HTTPAuthMode = "none"
	cfg.HTTPHeaders = map[string]string{"Content-Type": "text/plain"}
	service = New(cfg, nil)
	service.sleep = func(context.Context, time.Duration) error { return nil }
	if err := service.Send(context.Background(), Event{Status: "success", Domain: "example.com"}); err == nil {
		t.Fatal("覆盖安全请求头时应失败")
	}
}

func TestSendSupportsSessionLogin(t *testing.T) {
	var loginPayload map[string]string
	var sendCookie string
	var csrfToken string
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/login":
			if request.Method != http.MethodPost {
				t.Fatalf("登录请求方法错误：%s", request.Method)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &loginPayload); err != nil {
				t.Fatal(err)
			}
			if request.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("登录请求编码声明错误：%q", request.Header.Get("Content-Type"))
			}
			writer.Header().Add("Set-Cookie", "session=test-session; Path=/; HttpOnly")
			writer.Header().Add("Set-Cookie", "smail_csrf=test-csrf; Path=/")
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"errorNo":0,"errorMsg":"","data":{"account":"xuans"}}`))
		case "/api/email/send":
			sendCookie = request.Header.Get("Cookie")
			csrfToken = request.Header.Get("X-CSRF-Token")
			origin = request.Header.Get("Origin")
			writer.WriteHeader(http.StatusAccepted)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "session"
	cfg.HTTPCredentialRef = "secret://mail-session"
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"account":  "xuans",
		"password": " test-password ",
	}})
	if err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("会话认证通知发送失败：%v", err)
	}
	if loginPayload["account"] != "xuans" || loginPayload["password"] != " test-password " {
		t.Fatalf("登录请求字段错误：%v", loginPayload)
	}
	if !strings.Contains(sendCookie, "session=test-session") || csrfToken != "test-csrf" {
		t.Fatalf("发送请求未携带登录会话：cookie=%q csrf=%q", sendCookie, csrfToken)
	}
	if origin != server.URL {
		t.Fatalf("发送请求来源错误：%q", origin)
	}
}

func TestSendRejectsBusinessFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(`{"errorNo":100,"errorMsg":"参数错误"}`))
	}))
	defer server.Close()

	service := New(newTestConfig(server.URL), nil)
	err := service.Send(context.Background(), Event{Status: "success", Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "业务失败") {
		t.Fatalf("业务错误响应应被拒绝：%v", err)
	}
}

func TestSendRefreshesExpiredSession(t *testing.T) {
	loginCount := 0
	sendCount := 0
	var cookies []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/login":
			loginCount++
			writer.Header().Add("Set-Cookie", "session=session-"+strconv.Itoa(loginCount)+"; Path=/; HttpOnly")
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = writer.Write([]byte(`{"errorNo":0,"errorMsg":""}`))
		case "/api/email/send":
			sendCount++
			cookies = append(cookies, request.Header.Get("Cookie"))
			if sendCount == 1 {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "session"
	cfg.HTTPCredentialRef = "secret://mail-session"
	cfg.RetryCount = 1
	cfg.RetryIntervalsSecs = []int{1}
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"account":  "xuans",
		"password": "test-password",
	}})
	service.sleep = func(context.Context, time.Duration) error { return nil }
	if err := service.Send(context.Background(), Event{Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("会话失效后重新登录仍发送失败：%v", err)
	}
	if loginCount != 2 || sendCount != 2 || len(cookies) != 2 {
		t.Fatalf("会话刷新次数错误：login=%d send=%d cookies=%v", loginCount, sendCount, cookies)
	}
	if cookies[0] != "session=session-1" || cookies[1] != "session=session-2" {
		t.Fatalf("未使用刷新后的会话：%v", cookies)
	}
}

func TestSendRejectsEmptySessionPasswordWithoutTrimmingValidPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "session"
	cfg.HTTPCredentialRef = "secret://mail-session"
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"account":  "xuans",
		"password": "",
	}})
	err := service.Send(context.Background(), Event{Status: "success", Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "必须包含账号和密码") {
		t.Fatalf("空密码应在发起登录请求前被拒绝：%v", err)
	}
}

func TestSessionLoginPreservesWhitespacePassword(t *testing.T) {
	var loginPayload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/login" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &loginPayload); err != nil {
			t.Fatal(err)
		}
		writer.Header().Add("Set-Cookie", "session=whitespace-session; Path=/; HttpOnly")
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(`{"errorNo":0,"errorMsg":""}`))
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "session"
	cfg.HTTPCredentialRef = "secret://mail-session"
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"account":  "xuans",
		"password": " test-password ",
	}})
	if err := service.Send(context.Background(), Event{Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("保留空白密码的会话登录失败：%v", err)
	}
	if loginPayload["password"] != " test-password " {
		t.Fatalf("密码不应被 TrimSpace：%q", loginPayload["password"])
	}
}

func TestSessionLoginReturnsBusinessMessageWithoutCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(`{"errorNo":403,"errorMsg":"账号或密码错误"}`))
	}))
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.HTTPAuthMode = "session"
	cfg.HTTPCredentialRef = "secret://mail-session"
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"account":  "xuans",
		"password": "test-password",
	}})
	err := service.Send(context.Background(), Event{Status: "success", Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "错误码 403") || !strings.Contains(err.Error(), "账号或密码错误") {
		t.Fatalf("登录业务错误应包含安全诊断：%v", err)
	}
	if strings.Contains(err.Error(), "test-password") {
		t.Fatalf("登录错误不得包含密码：%v", err)
	}
}
