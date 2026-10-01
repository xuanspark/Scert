package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type resourceCertificateReader struct {
	reads []string
}

func (r *resourceCertificateReader) Current(_ context.Context, id string) (certificate.IssueResult, error) {
	r.reads = append(r.reads, id)
	return certificate.IssueResult{DomainID: id}, nil
}

func (r *resourceCertificateReader) ListVersions(_ context.Context, id string) ([]certificate.SavedResource, error) {
	r.reads = append(r.reads, id)
	return []certificate.SavedResource{}, nil
}

func newProjectResourceAPIFixture(t *testing.T) (projectMemberAPIFixture, *resourceCertificateReader) {
	t.Helper()
	f := newProjectMemberAPIFixture(t)
	domains := repository.NewSQLDomainRepository(f.db)
	servers := repository.NewSQLServerRepository(f.db)
	for _, suffix := range []string{"a", "b"} {
		item, err := domain.NewDomain(suffix+".example.test", domain.CertificateSingle, nil)
		if err != nil {
			t.Fatal(err)
		}
		item.ID, item.ProjectID = "domain-"+suffix, "project-"+suffix
		if err := domains.Create(t.Context(), item); err != nil {
			t.Fatal(err)
		}
		server := deployment.Server{
			ResourceScope: domain.ResourceScope{ProjectID: item.ProjectID},
			ID:            "server-" + suffix, Name: suffix + " 服务器", Address: "192.0.2.10", Port: 22, Username: "deploy",
			AuthMethod: deployment.SSHAuthPassword, PasswordRef: "secret://ssh/password/fixture", HostKeyFingerprint: "SHA256:test",
			CertificatePath: "/ssl/cert.pem", PrivateKeyPath: "/ssl/key.pem", WebServer: deployment.WebServerNginx,
		}
		if err := servers.CreateServer(t.Context(), server); err != nil {
			t.Fatal(err)
		}
		if err := servers.ReplaceDomainServers(t.Context(), item.ID, []string{server.ID}); err != nil {
			t.Fatal(err)
		}
	}
	reader := &resourceCertificateReader{}
	f.server.repository, f.server.servers, f.server.currentCertificate = domains, servers, reader
	return f, reader
}

func TestProjectResourceAPIListsUseProjectPermissions(t *testing.T) {
	f, reader := newProjectResourceAPIFixture(t)
	for _, tc := range []struct {
		actor string
		count int
	}{
		{"primary-super-admin", 2}, {"manager", 1}, {"viewer", 1}, {"global-admin", 0}, {"target", 0},
	} {
		for _, path := range []string{"/api/v1/domains", "/api/v1/servers", "/api/v1/certificates"} {
			reader.reads = nil
			response := serveUserRequest(f.server, f.cookies[tc.actor], http.MethodGet, path, "")
			var page struct {
				Items []json.RawMessage `json:"items"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Items == nil || len(page.Items) != tc.count {
				t.Fatalf("可见资源范围错误：%s，%s，%d，%s", tc.actor, path, response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Header().Get("Vary"), "Cookie") {
				t.Fatalf("资源响应缺少身份缓存隔离：%v", response.Header())
			}
			if tc.actor != "primary-super-admin" && strings.Contains(response.Body.String(), "project-b") {
				t.Fatalf("泄露其他项目：%s", response.Body.String())
			}
			if path == "/api/v1/certificates" && len(reader.reads) != tc.count {
				t.Fatalf("证书读取器访问了无权限域名：%+v", reader.reads)
			}
		}
	}
}

func TestProjectResourceAPIDetailsHideExistenceAndGateFiles(t *testing.T) {
	f, reader := newProjectResourceAPIFixture(t)
	for _, suffix := range []string{"a", "b", "missing"} {
		for _, path := range []string{
			"/api/v1/servers/server-" + suffix,
			"/api/v1/certificates/domain-" + suffix,
			"/api/v1/domains/domain-" + suffix + "/certificate/versions",
			"/api/v1/domains/domain-" + suffix + "/servers",
			"/api/v1/servers/server-" + suffix + "/domains",
		} {
			reader.reads = nil
			response := serveUserRequest(f.server, f.cookies["viewer"], http.MethodGet, path, "")
			want := http.StatusForbidden
			if suffix == "a" {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("资源详情权限错误：%s，%d，%s", path, response.Code, response.Body.String())
			}
			if suffix != "a" && (len(reader.reads) != 0 || !strings.Contains(response.Body.String(), "无权读取此项目资源")) {
				t.Fatalf("拒绝前访问了证书文件或泄露资源：%s，%+v", response.Body.String(), reader.reads)
			}
		}
	}
}

type resourceReadBoundary struct {
	repository.ProjectResourceReader
	before func()
}

func (r resourceReadBoundary) ListVisibleDomains(ctx context.Context, actor string) ([]domain.Domain, error) {
	r.before()
	return r.ProjectResourceReader.ListVisibleDomains(ctx, actor)
}

func TestProjectResourceAPIRechecksAfterSessionValidation(t *testing.T) {
	for _, mode := range []string{"revoke", "disable"} {
		t.Run(mode, func(t *testing.T) {
			f, reader := newProjectResourceAPIFixture(t)
			f.server.projectResources = resourceReadBoundary{ProjectResourceReader: f.members, before: func() {
				if mode == "revoke" {
					if err := f.members.RevokeProjectRole(t.Context(), "primary-super-admin", "project-a", "viewer", domain.ProjectRoleReadOnly); err != nil {
						t.Fatal(err)
					}
				} else if _, err := f.db.ExecContext(t.Context(), `UPDATE users SET status = 'disabled' WHERE id = 'viewer'`); err != nil {
					t.Fatal(err)
				}
			}}
			response := serveUserRequest(f.server, f.cookies["viewer"], http.MethodGet, "/api/v1/certificates", "")
			if response.Code != http.StatusOK || len(reader.reads) != 0 || !strings.Contains(response.Body.String(), `"items":[]`) {
				t.Fatalf("会话通过后撤权仍读取资源：%d，%s，%+v", response.Code, response.Body.String(), reader.reads)
			}
		})
	}
}

func TestProjectResourceAPIMissingDependencyFailsClosed(t *testing.T) {
	f, reader := newProjectResourceAPIFixture(t)
	f.server.projectResources = nil
	for _, path := range []string{
		"/api/v1/domains", "/api/v1/servers", "/api/v1/servers/server-a", "/api/v1/certificates",
		"/api/v1/certificates/domain-a", "/api/v1/domains/domain-a/certificate/versions", "/api/v1/domains/domain-a/servers",
		"/api/v1/servers/server-a/domains",
	} {
		response := serveUserRequest(f.server, f.cookies["primary-super-admin"], http.MethodGet, path, "")
		if response.Code != http.StatusServiceUnavailable || len(reader.reads) != 0 {
			t.Fatalf("缺失授权依赖时不应回退全量仓储：%s，%d，%s", path, response.Code, response.Body.String())
		}
	}
}

func TestProjectResourceAPIAuthenticationAndScopeForgery(t *testing.T) {
	f, reader := newProjectResourceAPIFixture(t)
	for _, path := range []string{"/api/v1/domains", "/api/v1/servers", "/api/v1/certificates"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("未认证读取未拒绝或缺少缓存保护：%s，%d", path, response.Code)
		}
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(f.cookies["viewer"])
		request.Header.Set("Origin", "https://foreign.example.test")
		response = httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("跨来源读取未拒绝：%s，%d", path, response.Code)
		}
	}
	for _, path := range []string{
		"/api/v1/servers/server-b?project_id=project-a&actor_id=primary-super-admin",
		"/api/v1/certificates/domain-b?project_id=project-a&actor_id=primary-super-admin",
	} {
		response := serveUserRequest(f.server, f.cookies["viewer"], http.MethodGet, path, "")
		if response.Code != http.StatusForbidden || len(reader.reads) != 0 {
			t.Fatalf("客户端伪造身份或项目生效：%s，%d", path, response.Code)
		}
	}
}
