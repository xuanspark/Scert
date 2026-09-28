package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 从真实应用装配入口登录并读取默认项目，防止只在 HTTP 测试夹具中注入新仓储。
func TestApplicationWiresProjectMemberAPI(t *testing.T) {
	cfg := readyApplicationConfig(t)
	application, err := NewWithConfigPath(t.Context(), cfg, filepath.Join(cfg.Paths.Config, "scert.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"admin","password":"Test1234!"}`))
	login.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	application.HTTP.Handler().ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("实际应用登录失败：%d，%s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/default/members?page_size=5", nil)
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	listed := httptest.NewRecorder()
	application.HTTP.Handler().ServeHTTP(listed, request)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"items":[]`) {
		t.Fatalf("实际应用未接入成员仓储或默认项目隐式增加成员：%d，%s", listed.Code, listed.Body.String())
	}
}
