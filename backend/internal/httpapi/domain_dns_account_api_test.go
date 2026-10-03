package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func newDomainDNSAPIFixture(t *testing.T) (projectMemberAPIFixture, domain.DNSAccount) {
	t.Helper()
	f, _ := newProjectResourceAPIFixture(t)
	item, err := f.members.CreateDNSAccount(t.Context(), "manager", domain.DNSAccount{
		ID: "account-a", ProjectID: "project-a", Name: "项目账户", Provider: "cloudflare", CredentialRef: "secret://dns/fixture-sensitive-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err = f.members.RecordDNSAccountVerification(t.Context(), "manager", item.ID, item.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	return f, item
}

func domainDNSBody(accountID, expectedID string, revision int64) string {
	return fmt.Sprintf(`{"dns_account_id":%q,"expected_binding_id":%q,"account_revision":%d}`, accountID, expectedID, revision)
}

func domainDNSRequest(t *testing.T, f projectMemberAPIFixture, actor, method, path, body string, status int) domain.DNSAccountBinding {
	t.Helper()
	response := serveUserRequest(f.server, f.cookies[actor], method, "/api/v1/domains/"+path+"/dns-account", body)
	if response.Code != status {
		t.Fatalf("域名 DNS 绑定响应错误：%s %s %s，实际 %d，期望 %d，%s", actor, method, path, response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
		t.Fatal("绑定响应缺少身份缓存隔离")
	}
	for _, secret := range []string{"secret://", "credential_ref", "fixture-sensitive-token", "internal-sensitive-error"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("绑定响应泄露秘密或内部错误")
		}
	}
	var binding domain.DNSAccountBinding
	if status == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &binding); err != nil {
			t.Fatal(err)
		}
	}
	return binding
}

func TestDomainDNSAccountAPILifecycle(t *testing.T) {
	f, account := newDomainDNSAPIFixture(t)
	read := domainDNSRequest(t, f, "viewer", http.MethodGet, "domain-a", "", http.StatusOK)
	if read.ID != "" || read.DomainID != "domain-a" || read.ProjectID != "project-a" {
		t.Fatal("未绑定时未返回域名归属")
	}
	body := domainDNSBody(account.ID, "", account.Revision)
	bound := domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", body, http.StatusOK)
	if bound.ID == "" || bound.DNSAccountID != account.ID || bound.UpdatedAt.IsZero() {
		t.Fatal("绑定未返回版本令牌")
	}
	read = domainDNSRequest(t, f, "viewer", http.MethodGet, "domain-a", "", http.StatusOK)
	if read.ID != bound.ID || read.DNSAccountID != account.ID {
		t.Fatal("读取与实际绑定不符")
	}
	for _, stale := range []string{body, domainDNSBody(account.ID, bound.ID, 1), domainDNSBody("", "", 0)} {
		domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", stale, http.StatusConflict)
	}
	cleared := domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", domainDNSBody("", bound.ID, 0), http.StatusOK)
	if cleared.ID != "" || cleared.DNSAccountID != "" {
		t.Fatal("解除后仍返回绑定")
	}
	rebound := domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", body, http.StatusOK)
	if rebound.ID == bound.ID {
		t.Fatal("重绑重复使用旧令牌")
	}
	domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", domainDNSBody("", bound.ID, 0), http.StatusConflict)
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='domain.dns_account' AND status='success' AND operator_id='manager'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("绑定成功审计数不符", count, err)
	}
}

func TestDomainDNSAccountAPIAuthorization(t *testing.T) {
	f, account := newDomainDNSAPIFixture(t)
	body := domainDNSBody(account.ID, "", account.Revision)
	for _, actor := range []string{"anonymous", "viewer", "global-admin", "target"} {
		status := http.StatusForbidden
		if actor == "anonymous" {
			status = http.StatusUnauthorized
		}
		domainDNSRequest(t, f, actor, http.MethodPut, "domain-a", body, status)
		if actor != "viewer" {
			domainDNSRequest(t, f, actor, http.MethodGet, "domain-a", "", status)
		}
	}
	for _, id := range []string{"domain-b", "missing-domain"} {
		domainDNSRequest(t, f, "manager", http.MethodGet, id, "", http.StatusForbidden)
		domainDNSRequest(t, f, "manager", http.MethodPut, id, body, http.StatusForbidden)
	}
	// 超级管理员也不能将同一账户绑定到另一个项目。
	domainDNSRequest(t, f, "primary-super-admin", http.MethodPut, "domain-b", body, http.StatusForbidden)
	if err := f.members.RevokeProjectRole(t.Context(), "primary-super-admin", "project-a", "manager", domain.ProjectRoleAdmin); err != nil {
		t.Fatal(err)
	}
	domainDNSRequest(t, f, "manager", http.MethodGet, "domain-a", "", http.StatusForbidden)
	domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", body, http.StatusForbidden)
}

func TestDomainDNSAccountAPIInputAndOrigin(t *testing.T) {
	f, account := newDomainDNSAPIFixture(t)
	for _, body := range []string{
		`{}`, `null`, `{"dns_account_id":null,"expected_binding_id":"","account_revision":0}`,
		`{"dns_account_id":"","expected_binding_id":null,"account_revision":0}`,
		`{"dns_account_id":"","expected_binding_id":"","account_revision":null}`,
		domainDNSBody("", "", 1), domainDNSBody(account.ID, "", 0), domainDNSBody(account.ID, "", 2147483648),
		domainDNSBody(account.ID, "invalid/id", account.Revision),
		`{"dns_account_id":"","expected_binding_id":"","account_revision":0,"actor_id":"primary-super-admin"}`,
		`{"dns_account_id":"","expected_binding_id":"","account_revision":0} {}`,
	} {
		domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", body, http.StatusBadRequest)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		response := serveUserRequest(f.server, f.cookies["manager"], method, "/api/v1/domains/domain-a/dns-account?project_id=project-b", domainDNSBody(account.ID, "", account.Revision))
		if response.Code != http.StatusBadRequest {
			t.Fatal("查询参数未拒绝", response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/domains/domain-a/dns-account", strings.NewReader(domainDNSBody(account.ID, "", account.Revision)))
	request.AddCookie(f.cookies["manager"])
	request.Header.Set("Origin", "https://untrusted.example.test")
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal("跨源写入未拒绝", response.Code)
	}
	binding := domainDNSRequest(t, f, "manager", http.MethodGet, "domain-a", "", http.StatusOK)
	if binding.ID != "" {
		t.Fatal("非法请求改变绑定")
	}
}

func TestDomainDNSAccountAPIDependencyAndState(t *testing.T) {
	for _, failure := range []string{"unverified", "disabled", "http_challenge", "active_task", "audit", "repository"} {
		t.Run(failure, func(t *testing.T) {
			f, account := newDomainDNSAPIFixture(t)
			status := http.StatusUnprocessableEntity
			switch failure {
			case "unverified":
				if _, err := f.db.Exec(`UPDATE dns_accounts SET verification_state='unverified'`); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := f.db.Exec(`UPDATE dns_accounts SET status='disabled'`); err != nil {
					t.Fatal(err)
				}
			case "http_challenge":
				if _, err := f.db.Exec(`UPDATE domains SET challenge_type='http-01' WHERE id='domain-a'`); err != nil {
					t.Fatal(err)
				}
			case "active_task":
				if _, err := f.db.Exec(`INSERT INTO tasks (id,domain_id,task_type,status,message) VALUES ('active-task','domain-a','certificate_issue','running','')`); err != nil {
					t.Fatal(err)
				}
				status = http.StatusConflict
			case "audit":
				if _, err := f.db.Exec(`CREATE TRIGGER reject_binding_audit BEFORE INSERT ON audit_logs WHEN NEW.action='domain.dns_account' BEGIN SELECT RAISE(ABORT,'internal-sensitive-error'); END`); err != nil {
					t.Fatal(err)
				}
				status = http.StatusServiceUnavailable
			case "repository":
				f.server.projectMembers = nil
				status = http.StatusServiceUnavailable
			}
			domainDNSRequest(t, f, "manager", http.MethodPut, "domain-a", domainDNSBody(account.ID, "", account.Revision), status)
			binding, err := f.members.GetDomainDNSAccountBinding(t.Context(), "manager", "domain-a")
			if err != nil || binding.ID != "" {
				t.Fatal("失败请求改变绑定", err)
			}
		})
	}
}
