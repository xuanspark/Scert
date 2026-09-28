package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// 走真实应用装配和会话登录，不能以测试夹具注入证明生产接线正确。
func TestApplicationWiresProjectLifecycleAPI(t *testing.T) {
	cfg := readyApplicationConfig(t)
	application, err := NewWithConfigPath(t.Context(), cfg, filepath.Join(cfg.Paths.Config, "scert.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	var cookies []*http.Cookie
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		application.HTTP.Handler().ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("实际应用请求失败：%s %s，%d，%s", method, path, response.Code, response.Body.String())
		}
		return response
	}
	login := request(http.MethodPost, "/api/v1/auth/login", `{"identifier":"admin","password":"Test1234!"}`, 200)
	cookies = login.Result().Cookies()
	for _, path := range []string{"/api/v1/domains", "/api/v1/servers", "/api/v1/certificates"} {
		resources := request(http.MethodGet, path, "", http.StatusOK)
		if !strings.Contains(resources.Body.String(), `"items":[]`) || resources.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("实际应用未接入空资源列表与缓存隔离：%s，%s", path, resources.Body.String())
		}
	}
	request(http.MethodGet, "/api/v1/servers/missing", "", http.StatusForbidden)
	request(http.MethodGet, "/api/v1/certificates/missing", "", http.StatusForbidden)
	created := request(http.MethodPost, "/api/v1/projects", `{"name":"实际应用项目","description":"接线验证"}`, 201)
	var item domain.Project
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil || item.ID == "" {
		t.Fatalf("创建未返回标识：%v", err)
	}
	path := "/api/v1/projects/" + item.ID
	request(http.MethodPut, path, `{"name":"修改后的项目"}`, 200)
	directory := request(http.MethodGet, "/api/v1/projects?page_size=30", "", 200)
	if !strings.Contains(directory.Body.String(), `"can_create_project":true`) || !strings.Contains(directory.Body.String(), "修改后的项目") {
		t.Fatalf("真实目录未更新或缺少能力：%s", directory.Body.String())
	}
	request(http.MethodDelete, path, fmt.Sprintf(`{"confirmation":%q}`, item.ID), 204)
	request(http.MethodPut, path, `{"name":"已删除项目"}`, 403)
}
