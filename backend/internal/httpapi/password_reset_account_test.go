package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestPasswordResetResolutionRejectsTrailingJSON(t *testing.T) {
	server := New(Dependencies{Config: config.Default(t.TempDir()), Repository: &stubDomainRepository{}})
	tests := []string{
		`{"identifier":"admin","unexpected":true}`,
		`{"identifier":"admin"}{"identifier":"again"}`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/password-reset/resolve", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("无效密码重置请求应返回 400，实际为 %d：%s", response.Code, response.Body.String())
		}
	}
}
