package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestProjectBindingAPIManyToManyAndPathConflict(t *testing.T) {
	f, _ := newProjectResourceAPIFixture(t)
	server, err := f.server.servers.GetServerByID(t.Context(), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath = "", "", ""
	if err := f.server.servers.UpdateServer(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	server.ID, server.Name = "server-c", "第二台共享服务器"
	if err := f.server.servers.CreateServer(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	item, err := domain.NewDomain("second.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.ID, item.ProjectID = "domain-c", "project-a"
	if err := repository.NewSQLDomainRepository(f.db).Create(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	put := func(id, body string, expected int) {
		t.Helper()
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodPut, "/api/v1/domains/"+id+"/servers", body)
		if response.Code != expected {
			t.Fatalf("多对多保存响应错误：%s，%d，%s", id, response.Code, response.Body.String())
		}
		if expected == http.StatusConflict && (!strings.Contains(response.Body.String(), "路径冲突") || strings.Contains(response.Body.String(), "second.example.test")) {
			t.Fatalf("冲突提示不明确或泄露其他域名：%s", response.Body.String())
		}
	}
	assertServerDomains := func(id string, expected []string) {
		t.Helper()
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodGet, "/api/v1/servers/"+id+"/domains", "")
		var result struct {
			Items []domain.Domain `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Items == nil {
			t.Fatalf("服务器反向关联读取响应错误：%d，%s", response.Code, response.Body.String())
		}
		ids := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		slices.Sort(ids)
		if !slices.Equal(ids, expected) {
			t.Fatalf("服务器反向关联不正确：%s，%v，期望 %v", id, ids, expected)
		}
	}
	assertBindings := func(id string, expected []string) {
		t.Helper()
		response := serveUserRequest(f.server, f.cookies["manager"], http.MethodGet, "/api/v1/domains/"+id+"/servers", "")
		var result struct {
			Items []deployment.Server `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Items == nil {
			t.Fatalf("多对多读取响应错误：%d，%s", response.Code, response.Body.String())
		}
		ids := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		slices.Sort(ids)
		if !slices.Equal(ids, expected) {
			t.Fatalf("改变一个域名后关联不正确：%s，%v，期望 %v", id, ids, expected)
		}
	}
	both := []string{"server-a", "server-c"}
	put("domain-a", `{"server_ids":["server-c","server-a","server-a"]}`, http.StatusOK)
	put("domain-c", `{"server_ids":["server-a","server-c"]}`, http.StatusOK)
	assertBindings("domain-a", both)
	assertBindings("domain-c", both)
	assertServerDomains("server-a", []string{"domain-a", "domain-c"})
	assertServerDomains("server-c", []string{"domain-a", "domain-c"})
	put("domain-a", `{"server_ids":["server-c"]}`, http.StatusOK)
	assertBindings("domain-a", both[1:])
	assertBindings("domain-c", both)
	assertServerDomains("server-a", []string{"domain-c"})
	assertServerDomains("server-c", []string{"domain-a", "domain-c"})
	put("domain-a", `{"server_ids":[]}`, http.StatusOK)
	assertBindings("domain-a", []string{})
	assertBindings("domain-c", both)
	put("domain-a", `{"server_ids":["server-a","server-c"]}`, http.StatusOK)
	put("domain-a", `{"server_ids":["server-b"]}`, http.StatusForbidden)
	assertBindings("domain-a", both)
	assertBindings("domain-c", both)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE servers SET certificate_path='/ssl/fixed/cert.pem' WHERE id='server-a'`); err != nil {
		t.Fatal(err)
	}
	put("domain-a", `{"server_ids":["server-a","server-c"]}`, http.StatusConflict)
	assertBindings("domain-a", both)
	assertBindings("domain-c", both)
	entry := f.lastAudit(t)
	if entry.Action != "domain.servers.replace" || entry.ProjectID != "project-a" || entry.Status != "failed" {
		t.Fatalf("路径冲突缺少项目失败审计：%+v", entry)
	}
}
