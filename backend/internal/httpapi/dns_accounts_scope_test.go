package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func newDNSAccountScopeFixture(t *testing.T) (projectMemberAPIFixture, *fakeSecretReaderStore) {
	t.Helper()
	f := newProjectMemberAPIFixture(t)
	f.server.config.DNS.Provider = "aliyun"
	f.server.config.DNS.ProjectID = "project-a"
	f.server.config.DNS.CredentialRef = "secret://dns/scope-test"
	secrets := &fakeSecretReaderStore{data: map[string]map[string]string{
		f.server.config.DNS.CredentialRef: {
			"access_key_id": "test-access-key-1234", "access_key_secret": "test-secret-never-return",
		},
	}}
	f.server.secrets = secrets
	return f, secrets
}

func TestDNSAccountScopePermissions(t *testing.T) {
	f, secrets := newDNSAccountScopeFixture(t)
	for _, tc := range []struct {
		actor string
		count int
	}{
		{"primary-super-admin", 1}, {"manager", 1}, {"viewer", 1}, {"global-admin", 0}, {"target", 0},
	} {
		t.Run(tc.actor, func(t *testing.T) {
			secrets.getCalls = 0
			response := serveUserRequest(f.server, f.cookies[tc.actor], http.MethodGet, "/api/v1/dns/accounts", "")
			var page struct {
				Items []dnsAccountView `json:"items"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Items == nil || len(page.Items) != tc.count {
				t.Fatalf("DNS 账户项目可见范围错误：%s，%d，%s", tc.actor, response.Code, response.Body.String())
			}
			if secrets.getCalls != tc.count {
				t.Fatalf("无权访问的请求读取了凭据仓库：%s，%d", tc.actor, secrets.getCalls)
			}
			if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
				t.Fatal("DNS 账户响应缺少身份缓存隔离")
			}
			for _, forbidden := range []string{"test-access-key-1234", "test-secret-never-return", "secret://dns/scope-test"} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatal("DNS 账户列表泄露完整凭据或内部引用")
				}
			}
			if tc.count == 1 && (page.Items[0].ProjectID != "project-a" || page.Items[0].AccountIdentifier != "test****1234") {
				t.Fatalf("授权响应丢失真实归属或脱敏标识：%s", response.Body.String())
			}
		})
	}
	// 请求参数不能冒充其他身份或改变配置中的真实项目归属。
	f.server.config.DNS.ProjectID = "project-b"
	secrets.getCalls = 0
	response := serveUserRequest(f.server, f.cookies["manager"], http.MethodGet, "/api/v1/dns/accounts?project_id=project-a&actor_id=primary-super-admin", "")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"items":[]}` || secrets.getCalls != 0 {
		t.Fatalf("跨项目请求没有在读取凭据之前拒绝：%d，%s", response.Code, response.Body.String())
	}
}

func TestDNSAccountScopeRevocation(t *testing.T) {
	f, secrets := newDNSAccountScopeFixture(t)
	if err := f.members.GrantProjectRole(t.Context(), "primary-super-admin", "project-a", "global-admin", domain.ProjectRoleDNSAdmin); err != nil {
		t.Fatal(err)
	}
	response := serveUserRequest(f.server, f.cookies["global-admin"], http.MethodGet, "/api/v1/dns/accounts", "")
	if response.Code != http.StatusOK || secrets.getCalls != 1 {
		t.Fatalf("显式授予 DNS 角色后不可读取：%d，%s", response.Code, response.Body.String())
	}
	if err := f.members.RevokeProjectRole(t.Context(), "primary-super-admin", "project-a", "global-admin", domain.ProjectRoleDNSAdmin); err != nil {
		t.Fatal(err)
	}
	response = serveUserRequest(f.server, f.cookies["global-admin"], http.MethodGet, "/api/v1/dns/accounts", "")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"items":[]}` || secrets.getCalls != 1 {
		t.Fatalf("撤权后原会话仍可读取 DNS 账户：%d，%s", response.Code, response.Body.String())
	}
}

func TestDNSAccountScopeDefaultsAndDependencies(t *testing.T) {
	for _, scenario := range []string{"default", "missing_project", "missing_authorizer", "authorizer_error", "database_error", "anonymous"} {
		t.Run(scenario, func(t *testing.T) {
			f, secrets := newDNSAccountScopeFixture(t)
			cookie := f.cookies["primary-super-admin"]
			want := http.StatusOK
			switch scenario {
			case "default":
				f.server.config.DNS.ProjectID = "  "
			case "missing_project":
				f.server.config.DNS.ProjectID = "missing"
			case "missing_authorizer":
				f.server.projectMembers, f.server.projectDomains = nil, nil
				want = http.StatusServiceUnavailable
			case "authorizer_error":
				f.server.projectMembers = nil
				f.server.projectDomains = projectDomainRepositoryStub{authorizeErr: errors.New("internal-sensitive-diagnostic")}
				want = http.StatusServiceUnavailable
			case "database_error":
				// 认证仍使用正常账户仓储，只让项目授权的数据库读取失败。
				f.server.projectMembers = nil
				f.server.projectDomains = projectDomainRepositoryStub{authorize: func(ctx context.Context, actor, project string, permission domain.ProjectPermission) error {
					if err := f.db.Close(); err != nil {
						return err
					}
					return f.members.AuthorizeProject(ctx, actor, project, permission)
				}}
				want = http.StatusServiceUnavailable
			case "anonymous":
				cookie = nil
				want = http.StatusUnauthorized
			}
			response := serveUserRequest(f.server, cookie, http.MethodGet, "/api/v1/dns/accounts", "")
			if response.Code != want || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
				t.Fatalf("DNS 列表依赖或缓存隔离错误：%d，%s", response.Code, response.Body.String())
			}
			if scenario == "default" {
				if secrets.getCalls != 1 || !strings.Contains(response.Body.String(), `"project_id":"default"`) {
					t.Fatal("旧配置没有按默认项目授权")
				}
				return
			}
			if secrets.getCalls != 0 || strings.Contains(response.Body.String(), "project-a") || strings.Contains(response.Body.String(), "internal-sensitive-diagnostic") || strings.Contains(response.Body.String(), "account_identifier") {
				t.Fatalf("未获可靠授权仍读取凭据或泄露元数据：%s", response.Body.String())
			}
		})
	}
}
