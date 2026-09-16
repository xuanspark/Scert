package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsverification"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type fakeBootstrapDatabaseVerifier struct {
	result DatabaseVerificationResult
	err    error
	calls  int
}

func (verifier *fakeBootstrapDatabaseVerifier) Verify(_ context.Context, _ DatabaseInput) (DatabaseVerificationResult, error) {
	verifier.calls++
	return verifier.result, verifier.err
}

type fakeBootstrapDNSVerifier struct {
	result setup.DNSVerificationResult
	err    error
	calls  int
	input  setup.DNSVerificationInput
}

func (verifier *fakeBootstrapDNSVerifier) Verify(_ context.Context, input setup.DNSVerificationInput) (setup.DNSVerificationResult, error) {
	verifier.calls++
	verifier.input = input
	return verifier.result, verifier.err
}

func TestBootstrapRouterRequiresSessionForProtectedAPIs(t *testing.T) {
	sessions, _, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	database := &fakeBootstrapDatabaseVerifier{}
	dns := &fakeBootstrapDNSVerifier{}
	router := NewRouter(sessions, database, dns, nil)

	for _, item := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/bootstrap/database/test", `{"driver":"sqlite"}`},
		{http.MethodGet, "/api/v1/bootstrap/dns/providers", ""},
		{http.MethodPost, "/api/v1/bootstrap/dns/test", `{"provider":"aliyun"}`},
		{http.MethodPost, "/api/v1/bootstrap/complete", `{}`},
	} {
		response := performBootstrapRequest(router, item.method, item.path, item.body, nil, "192.0.2.10:50000")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("未认证请求 %s 返回状态不正确：%d body=%s", item.path, response.Code, response.Body.String())
		}
		assertBootstrapSecurityHeaders(t, response)
	}
	if database.calls != 0 || dns.calls != 0 {
		t.Fatal("未认证请求调用了底层验证服务")
	}
}

func TestBootstrapRouterExchangesTokenOnceAndSetsSecureCookie(t *testing.T) {
	sessions, token, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(sessions, nil, nil, nil)
	body := fmt.Sprintf(`{"token":%q}`, token)
	first := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/session", body, map[string]string{
		"Content-Type":    "application/json",
		"X-Forwarded-For": "203.0.113.99",
	}, "192.0.2.20:51000")
	if first.Code != http.StatusOK {
		t.Fatalf("兑换初始化令牌失败：%d body=%s", first.Code, first.Body.String())
	}
	cookies := first.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("初始化会话 Cookie 数量不正确：%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || cookie.Value == "" || cookie.Path != "/api/v1/bootstrap" ||
		!cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("初始化会话 Cookie 安全属性不完整：%+v", cookie)
	}
	if strings.Contains(first.Body.String(), token) || strings.Contains(first.Body.String(), cookie.Value) {
		t.Fatal("初始化令牌或 Cookie 值被写入响应正文")
	}
	second := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/session", body, map[string]string{
		"Content-Type": "application/json",
	}, "192.0.2.20:51001")
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("一次性令牌被重复兑换：%d body=%s", second.Code, second.Body.String())
	}
}

func TestBootstrapRouterIgnoresForwardedHeadersForRateLimit(t *testing.T) {
	sessions, _, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(sessions, nil, nil, nil)
	for index := 0; index < bootstrapSourceAttemptLimit; index++ {
		response := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/session", `{"token":"wrong"}`, map[string]string{
			"Content-Type":    "application/json",
			"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", index+1),
			"X-Real-IP":       fmt.Sprintf("198.51.100.%d", index+1),
		}, "192.0.2.44:52000")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误令牌请求返回异常：%d", index+1, response.Code)
		}
	}
	limited := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/session", `{"token":"wrong"}`, map[string]string{
		"Content-Type":    "application/json",
		"X-Forwarded-For": "203.0.113.250",
	}, "192.0.2.44:52001")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("伪造转发头绕过来源限流：%d body=%s", limited.Code, limited.Body.String())
	}
}

func TestBootstrapRouterRejectsInvalidJSONBodies(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mediaType  string
		wantStatus int
	}{
		{name: "未知字段", body: `{"token":"x","extra":true}`, mediaType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "多段JSON", body: `{"token":"x"}{"token":"y"}`, mediaType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "错误类型", body: `{"token":"x"}`, mediaType: "text/plain", wantStatus: http.StatusBadRequest},
		{name: "超大请求", body: `{"token":"` + strings.Repeat("x", bootstrapSessionRequestLimit) + `"}`, mediaType: "application/json", wantStatus: http.StatusBadRequest},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			sessions, _, err := NewSessionManager()
			if err != nil {
				t.Fatal(err)
			}
			response := performBootstrapRequest(NewRouter(sessions, nil, nil, nil), http.MethodPost, "/api/v1/bootstrap/session", item.body, map[string]string{
				"Content-Type": item.mediaType,
			}, "192.0.2.50:53000")
			if response.Code != item.wantStatus {
				t.Fatalf("状态不正确：%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBootstrapRouterOnlyListsAndAcceptsAliyun(t *testing.T) {
	sessions, cookie := authenticatedBootstrapSession(t)
	dns := &fakeBootstrapDNSVerifier{}
	router := NewRouter(sessions, nil, dns, nil)

	providers := performBootstrapRequest(router, http.MethodGet, "/api/v1/bootstrap/dns/providers", "", nil, "192.0.2.60:54000", cookie)
	if providers.Code != http.StatusOK {
		t.Fatalf("读取 DNS 服务商失败：%d body=%s", providers.Code, providers.Body.String())
	}
	var payload struct {
		Items []dnsprovider.Descriptor `json:"items"`
	}
	if err := json.Unmarshal(providers.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != dnsprovider.Aliyun {
		t.Fatalf("首次初始化返回了非阿里云服务商：%+v", payload.Items)
	}

	rejected := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/dns/test", `{
		"provider":"cloudflare",
		"test_domain":"example.com",
		"credentials":{"api_token":"secret"}
	}`, map[string]string{"Content-Type": "application/json"}, "192.0.2.60:54001", cookie)
	if rejected.Code != http.StatusBadRequest || dns.calls != 0 {
		t.Fatalf("非阿里云请求未在调用验证器前拒绝：status=%d calls=%d", rejected.Code, dns.calls)
	}
}

func TestBootstrapRouterResponsesDoNotLeakDatabaseOrDNSCredentials(t *testing.T) {
	sessions, cookie := authenticatedBootstrapSession(t)
	database := &fakeBootstrapDatabaseVerifier{result: DatabaseVerificationResult{
		Config: config.DatabaseConfig{
			Driver:        "mysql",
			DSN:           "scert_user:database-password@tcp(db.example.test:3306)/scert",
			CredentialRef: "secret://database/private-reference",
		},
		CredentialRef:     "secret://database/private-reference",
		VerificationToken: "database-verification-token",
		ExpiresAt:         time.Now().UTC().Add(time.Minute),
	}}
	dns := &fakeBootstrapDNSVerifier{result: setup.DNSVerificationResult{
		CredentialRef:     "secret://dns/aliyun/private-reference",
		VerificationToken: "dns-verification-token",
		ExpiresAt:         time.Now().UTC().Add(time.Minute),
		TestReport: dnsverification.Report{RecordName: "_scert.example.com.", Stages: []dnsverification.Stage{
			{Name: "查询记录", Status: "completed", Message: "已完成安全验证。"},
		}},
	}}
	router := NewRouter(sessions, database, dns, nil)

	databaseResponse := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/database/test", `{
		"driver":"mysql",
		"host":"db.example.test",
		"database":"scert",
		"username":"scert_user",
		"password":"database-password",
		"tls_mode":"true"
	}`, map[string]string{"Content-Type": "application/json"}, "192.0.2.70:55000", cookie)
	assertResponseExcludes(t, databaseResponse, "scert_user", "database-password", "secret://", "private-reference")
	if databaseResponse.Code != http.StatusOK || !strings.Contains(databaseResponse.Body.String(), "database-verification-token") {
		t.Fatalf("数据库验证响应不正确：%d body=%s", databaseResponse.Code, databaseResponse.Body.String())
	}

	dnsResponse := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/dns/test", `{
		"provider":"aliyun",
		"test_domain":"example.com",
		"credentials":{
			"access_key_id":"test-access-id",
			"access_key_secret":"test-access-secret",
			"security_token":"test-security-token"
		}
	}`, map[string]string{"Content-Type": "application/json"}, "192.0.2.70:55001", cookie)
	assertResponseExcludes(t, dnsResponse, "test-access-id", "test-access-secret", "test-security-token", "secret://", "private-reference")
	if dnsResponse.Code != http.StatusOK || dns.calls != 1 || dns.input.ProviderID != dnsprovider.Aliyun {
		t.Fatalf("阿里云 DNS 验证响应不正确：status=%d calls=%d input=%+v body=%s", dnsResponse.Code, dns.calls, dns.input, dnsResponse.Body.String())
	}
}

func TestBootstrapRouterRedactsSensitiveUnderlyingErrors(t *testing.T) {
	sessions, cookie := authenticatedBootstrapSession(t)
	databaseSecret := "database-password"
	dnsSecret := "ram-access-secret"
	database := &fakeBootstrapDatabaseVerifier{err: errors.New("connect failed with password " + databaseSecret)}
	report := dnsverification.Report{RecordName: "_scert.example.com.", Stages: []dnsverification.Stage{{
		Name: "创建记录", Status: "failed", Message: "provider returned " + dnsSecret,
	}}}
	dns := &fakeBootstrapDNSVerifier{result: setup.DNSVerificationResult{TestReport: report}, err: &setup.DNSVerificationError{
		Report: report,
		Err:    errors.New("aliyun rejected " + dnsSecret),
	}}
	router := NewRouter(sessions, database, dns, nil)

	databaseResponse := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/database/test", fmt.Sprintf(`{
		"driver":"mysql","host":"db.example.test","database":"scert",
		"username":"scert_user","password":%q,"tls_mode":"true"
	}`, databaseSecret), map[string]string{"Content-Type": "application/json"}, "192.0.2.80:56000", cookie)
	if databaseResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("数据库错误状态不正确：%d", databaseResponse.Code)
	}
	assertResponseExcludes(t, databaseResponse, databaseSecret, "connect failed")

	dnsResponse := performBootstrapRequest(router, http.MethodPost, "/api/v1/bootstrap/dns/test", fmt.Sprintf(`{
		"provider":"aliyun","test_domain":"example.com",
		"credentials":{"access_key_id":"id","access_key_secret":%q}
	}`, dnsSecret), map[string]string{"Content-Type": "application/json"}, "192.0.2.80:56001", cookie)
	if dnsResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("DNS 错误状态不正确：%d body=%s", dnsResponse.Code, dnsResponse.Body.String())
	}
	assertResponseExcludes(t, dnsResponse, dnsSecret, "aliyun rejected")
}

func TestBootstrapRouterStatusAndUnknownAPIAreSafeJSON(t *testing.T) {
	router := NewRouter(nil, nil, nil, nil)
	status := performBootstrapRequest(router, http.MethodGet, "/api/v1/bootstrap/status", "", nil, "192.0.2.90:57000")
	if status.Code != http.StatusOK || status.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("Bootstrap 状态响应不正确：%d headers=%v", status.Code, status.Header())
	}
	assertResponseExcludes(t, status, "token=", SessionCookieName, "bootstrap-url", "secret://")
	assertBootstrapSecurityHeaders(t, status)

	notFound := performBootstrapRequest(router, http.MethodGet, "/api/v1/unknown", "", nil, "192.0.2.90:57001")
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("未知 API 未返回 JSON 404：%d headers=%v body=%s", notFound.Code, notFound.Header(), notFound.Body.String())
	}
}

func authenticatedBootstrapSession(t *testing.T) (*SessionManager, *http.Cookie) {
	t.Helper()
	sessions, token, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Exchange(token, "192.0.2.1:49000")
	if err != nil {
		t.Fatal(err)
	}
	return sessions, BootstrapSessionCookie(session.Token, session.ExpiresAt)
}

func performBootstrapRequest(
	router http.Handler,
	method string,
	path string,
	body string,
	headers map[string]string,
	remoteAddr string,
	cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://bootstrap.example.test"+path, strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertBootstrapSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Referrer-Policy") != "no-referrer" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" ||
		response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("Bootstrap 安全响应头不完整：%v", response.Header())
	}
}

func assertResponseExcludes(t *testing.T, response *httptest.ResponseRecorder, values ...string) {
	t.Helper()
	body := response.Body.String()
	for _, value := range values {
		if strings.Contains(body, value) {
			t.Fatalf("响应泄露敏感内容 %q：%s", value, body)
		}
	}
}
