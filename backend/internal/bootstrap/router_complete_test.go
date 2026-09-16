package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type fakeBootstrapCompleter struct {
	result CompleteResult
	err    error
	calls  int
	input  CompleteRequest
}

func (completer *fakeBootstrapCompleter) Complete(ctx context.Context, input CompleteRequest) (CompleteResult, error) {
	completer.calls++
	completer.input = input
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > bootstrapCompletionTimeout {
		return CompleteResult{}, errors.New("missing completion deadline")
	}
	return completer.result, completer.err
}

func TestBootstrapCompleteRequiresSessionAndPost(t *testing.T) {
	sessions, cookie := authenticatedBootstrapSession(t)
	completer := &fakeBootstrapCompleter{}
	router := NewRouter(sessions, nil, nil, completer)
	for _, item := range []struct {
		method string
		cookie *http.Cookie
		status int
	}{
		{http.MethodPost, nil, http.StatusUnauthorized},
		{http.MethodPost, &http.Cookie{Name: SessionCookieName, Value: "invalid"}, http.StatusUnauthorized},
		{http.MethodGet, cookie, http.StatusMethodNotAllowed},
		{http.MethodPut, cookie, http.StatusMethodNotAllowed},
	} {
		var cookies []*http.Cookie
		if item.cookie != nil {
			cookies = append(cookies, item.cookie)
		}
		response := performBootstrapRequest(router, item.method, "/api/v1/bootstrap/complete", `{}`, nil, "192.0.2.1:5000", cookies...)
		if response.Code != item.status || completer.calls != 0 {
			t.Fatalf("非法请求进入提交服务：状态=%d，调用=%d", response.Code, completer.calls)
		}
		if item.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != http.MethodPost {
			t.Fatal("方法错误应明确允许 POST")
		}
		assertBootstrapSecurityHeaders(t, response)
	}
}

func TestBootstrapCompleteRejectsInvalidBodiesBeforeSideEffects(t *testing.T) {
	for _, item := range []struct {
		name, body, contentType string
	}{
		{"unknown", `{"extra":true}`, "application/json"},
		{"trailing", `{} {}`, "application/json"},
		{"trailing_null", `{} null`, "application/json"},
		{"null", `null`, "application/json"},
		{"array", `[]`, "application/json"},
		{"empty", ``, "application/json"},
		{"wrong_type", `{"server_port":"8443"}`, "application/json"},
		{"oversized", `{"admin_password":"` + strings.Repeat("x", bootstrapRequestLimit) + `"}`, "application/json"},
		{"media_type", `{}`, "text/plain"},
	} {
		t.Run(item.name, func(t *testing.T) {
			sessions, cookie := authenticatedBootstrapSession(t)
			completer := &fakeBootstrapCompleter{}
			response := performBootstrapRequest(NewRouter(sessions, nil, nil, completer), http.MethodPost,
				"/api/v1/bootstrap/complete", item.body, map[string]string{"Content-Type": item.contentType}, "192.0.2.1:5000", cookie)
			if response.Code != http.StatusBadRequest || completer.calls != 0 {
				t.Fatalf("无效正文未提前拒绝：状态=%d，调用=%d", response.Code, completer.calls)
			}
		})
	}
}

func TestBootstrapCompleteMapsErrorsWithoutLeakingSecrets(t *testing.T) {
	secret := "sensitive-password-token-reference"
	for _, item := range []struct {
		name   string
		err    error
		status int
	}{
		{"busy", ErrCompleteBusy, http.StatusConflict},
		{"done", ErrCompleteDone, http.StatusConflict},
		{"input", completeError(CompleteErrorInput, secret, errors.New(secret)), http.StatusBadRequest},
		{"verification", completeError(CompleteErrorVerification, secret, errors.New(secret)), http.StatusUnprocessableEntity},
		{"dependency", completeError(CompleteErrorDependency, secret, errors.New(secret)), http.StatusBadGateway},
		{"internal", completeError(CompleteErrorInternal, secret, errors.New(secret)), http.StatusInternalServerError},
		{"unknown", errors.New(secret), http.StatusInternalServerError},
	} {
		t.Run(item.name, func(t *testing.T) {
			sessions, cookie := authenticatedBootstrapSession(t)
			completer := &fakeBootstrapCompleter{err: item.err}
			response := performBootstrapRequest(NewRouter(sessions, nil, nil, completer), http.MethodPost,
				"/api/v1/bootstrap/complete", `{}`, nil, "192.0.2.1:5000", cookie)
			if response.Code != item.status || completer.calls != 1 {
				t.Fatalf("错误映射不正确：%d，%s", response.Code, response.Body.String())
			}
			assertResponseExcludes(t, response, secret)
			if !sessions.Authenticate(cookie.Value) {
				t.Fatal("提交失败不能撤销可供重新验证的初始化会话")
			}
		})
	}
}

func TestBootstrapCompleteUnavailableAndInvalidResult(t *testing.T) {
	for _, available := range []bool{false, true} {
		sessions, cookie := authenticatedBootstrapSession(t)
		var completer bootstrapCompleter
		status := http.StatusServiceUnavailable
		if available {
			completer = &fakeBootstrapCompleter{}
			status = http.StatusInternalServerError
		}
		response := performBootstrapRequest(NewRouter(sessions, nil, nil, completer), http.MethodPost,
			"/api/v1/bootstrap/complete", `{}`, nil, "192.0.2.1:5000", cookie)
		if response.Code != status || !sessions.Authenticate(cookie.Value) {
			t.Fatalf("未提交状态不应误报成功或撤销会话：%d", response.Code)
		}
	}
}

func TestBootstrapCompleteCommitsAndClearsCookie(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	request := fixture.request("admin.example.test")
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(fixture.sessions, fixture.database, fixture.dns, fixture.completer(nil))
	cookie := BootstrapSessionCookie(fixture.sessionCookie, time.Now().Add(time.Hour))
	response := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/complete", string(body),
		map[string]string{"Content-Type": "application/json"}, "192.0.2.1:5000", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("初始化 HTTP 提交失败：%d，%s", response.Code, response.Body.String())
	}
	var result CompleteResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Completed || result.ManagementURL != "https://admin.example.test:8443/" {
		t.Fatalf("管理入口响应不正确：%+v，%v", result, err)
	}
	assertResponseExcludes(t, response, request.AdminPassword, request.DatabaseVerificationToken, request.DNSVerificationToken, "secret://", "test-secret")
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge != -1 ||
		!cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/api/v1/bootstrap" || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("初始化成功未安全清除 Cookie：%+v", cookies)
	}
	stored, err := config.Load(fixture.configPath)
	if err != nil || !stored.SetupReady || !fixture.store.has(fixture.dnsRef) {
		t.Fatalf("HTTP 提交未保留完整正式配置和凭据：%v", err)
	}
	for _, path := range []string{"/api/v1/bootstrap/complete", "/api/v1/bootstrap/database/test", "/api/v1/bootstrap/dns/test"} {
		replayed := performBootstrapRequest(router, http.MethodPost, path, `{}`, nil, "192.0.2.1:5000", cookie)
		if replayed.Code != http.StatusUnauthorized {
			t.Fatalf("提交后旧会话仍可调用 %s：%d", path, replayed.Code)
		}
	}
}
