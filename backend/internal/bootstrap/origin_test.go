package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapRejectsCrossOriginBeforeTokenExchange(t *testing.T) {
	for _, origin := range []string{"https://attacker.example", "http://127.0.0.1:8443", "null", "https://127.0.0.1:8443/path"} {
		t.Run(origin, func(t *testing.T) {
			sessions, token, err := NewSessionManager()
			if err != nil {
				t.Fatal(err)
			}
			router := NewRouter(sessions, nil, nil, nil)
			request := httptest.NewRequest(http.MethodPost, "https://127.0.0.1:8443/api/v1/bootstrap/session", strings.NewReader(`{"token":"`+token+`"}`))
			request.Header.Set("Origin", origin)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("跨源写入未拒绝：%d", response.Code)
			}
			if _, err := sessions.Exchange(token, "127.0.0.1"); err != nil {
				t.Fatalf("被拒绝的请求消耗了令牌：%v", err)
			}
		})
	}
}

func TestBootstrapSameOriginPolicy(t *testing.T) {
	for _, item := range []struct {
		origin  string
		site    string
		allowed bool
	}{
		{"https://127.0.0.1:8443", "same-origin", true},
		{"", "", true},
		{"", "cross-site", false},
		{"https://127.0.0.1:8443", "same-site", false},
		{"https://user@127.0.0.1:8443", "", false},
	} {
		request := httptest.NewRequest(http.MethodPost, "https://127.0.0.1:8443/", nil)
		request.Header.Set("Origin", item.origin)
		request.Header.Set("Sec-Fetch-Site", item.site)
		request.Header.Set("X-Forwarded-Host", "attacker.example")
		if bootstrapSameOrigin(request) != item.allowed {
			t.Fatalf("来源策略不符：%+v", item)
		}
	}
}
