package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsverification"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type accountVerifierFunc func(context.Context, string, string, map[string]string) (dnsverification.Report, error)

func (f accountVerifierFunc) Verify(ctx context.Context, provider, name string, credentials map[string]string) (dnsverification.Report, error) {
	return f(ctx, provider, name, credentials)
}

const managedDNSCreateBody = `{"name":"项目 DNS","project_id":"project-a","provider":"cloudflare","credentials":{"api_token":"fixture-sensitive-token"}}`

func managedDNSRequest(t *testing.T, f projectMemberAPIFixture, actor, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	response := serveUserRequest(f.server, f.cookies[actor], method, "/api/v1/dns/accounts"+path, body)
	if response.Code != status {
		t.Fatalf("DNS 请求状态错误：%s %s %s，实际 %d 期望 %d，%s", actor, method, path, response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" {
		t.Fatal("DNS 账户响应未隔离缓存")
	}
	for _, value := range []string{"fixture-sensitive-token", "rotated-sensitive-token", "secret://", "credential_ref", "internal-sensitive-error"} {
		if strings.Contains(response.Body.String(), value) {
			t.Fatalf("DNS 账户响应暴露内部敏感内容：%s", value)
		}
	}
	return response
}

func newManagedDNSFixture(t *testing.T) (projectMemberAPIFixture, *fakeSecretReaderStore, domain.DNSAccount) {
	t.Helper()
	f := newProjectMemberAPIFixture(t)
	f.server.config.DNS.CredentialRef = ""
	secrets := &fakeSecretReaderStore{}
	f.server.secrets = secrets
	response := managedDNSRequest(t, f, "manager", http.MethodPost, "", managedDNSCreateBody, http.StatusCreated)
	var item domain.DNSAccount
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || item.ID == "" || item.OwnerUserID != "manager" || item.VerificationState != domain.DNSAccountUnverified || item.Revision != 1 {
		t.Fatal("创建未固定真实归属和验证状态", err)
	}
	return f, secrets, item
}

func TestManagedDNSAccountLifecycle(t *testing.T) {
	f, secrets, item := newManagedDNSFixture(t)
	path := "/" + item.ID
	verifications := 0
	f.server.dnsAccountVerifier = accountVerifierFunc(func(ctx context.Context, provider, name string, credentials map[string]string) (dnsverification.Report, error) {
		verifications++
		if provider != "cloudflare" || name != "example.test" || credentials["api_token"] != "fixture-sensitive-token" {
			t.Fatal("验证未使用数据库账户对应的凭据")
		}
		if _, exists := ctx.Deadline(); !exists {
			t.Fatal("在线验证没有超时上限")
		}
		return dnsverification.Report{}, nil
	})
	managedDNSRequest(t, f, "manager", http.MethodPost, path+"/verify", `{"revision":1,"test_domain":"example.test"}`, http.StatusOK)
	stored, err := f.members.GetDNSAccountForUse(t.Context(), "manager", item.ID, "project-a")
	if err != nil || stored.Revision != 2 || stored.CredentialRef == "" || verifications != 1 {
		t.Fatal("验证结果未写入正确版本", err)
	}
	oldReference := stored.CredentialRef
	managedDNSRequest(t, f, "manager", http.MethodPut, path, `{"revision":2,"name":"更名后的账户"}`, http.StatusOK)
	managedDNSRequest(t, f, "manager", http.MethodPut, path, `{"revision":2,"name":"旧版本"}`, http.StatusConflict)
	managedDNSRequest(t, f, "manager", http.MethodPut, path+"/status", `{"revision":3,"enabled":false}`, http.StatusOK)
	managedDNSRequest(t, f, "manager", http.MethodPost, path+"/verify", `{"revision":4,"test_domain":"example.test"}`, http.StatusForbidden)
	if verifications != 1 {
		t.Fatal("已禁用账户仍调用服务商")
	}
	managedDNSRequest(t, f, "manager", http.MethodPut, path+"/credentials", `{"revision":4,"credentials":{"api_token":"rotated-sensitive-token"}}`, http.StatusOK)
	managedDNSRequest(t, f, "manager", http.MethodPut, path+"/status", `{"revision":5,"enabled":true}`, http.StatusOK)
	stored, err = f.members.GetDNSAccountForVerification(t.Context(), "manager", item.ID)
	if err != nil || stored.CredentialRef == oldReference || stored.VerificationState != domain.DNSAccountUnverified || len(secrets.data) != 2 {
		t.Fatal("轮换没有生成新引用、保留旧引用或清除验证状态", err)
	}
	f.server.dnsAccountVerifier = accountVerifierFunc(func(_ context.Context, _, _ string, credentials map[string]string) (dnsverification.Report, error) {
		if credentials["api_token"] != "rotated-sensitive-token" {
			t.Fatal("验证仍使用旧凭据")
		}
		return dnsverification.Report{}, errors.New("internal-sensitive-error")
	})
	managedDNSRequest(t, f, "manager", http.MethodPost, path+"/verify", `{"revision":6,"test_domain":"example.test"}`, http.StatusUnprocessableEntity)
	stored, err = f.members.GetVisibleDNSAccount(t.Context(), "manager", item.ID)
	if err != nil || stored.VerificationState != domain.DNSAccountVerificationFailed || stored.Revision != 7 {
		t.Fatal("服务商失败没有记录到对应版本", err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE resource_type='dns_account' AND (message LIKE '%token%' OR message LIKE '%secret://%')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("审计泄露凭据", err)
	}
}

func TestManagedDNSAccountPermissionsAndListing(t *testing.T) {
	f, secrets, item := newManagedDNSFixture(t)
	for _, actor := range []string{"viewer", "global-admin", "target", "anonymous"} {
		status := http.StatusForbidden
		if actor == "anonymous" {
			status = http.StatusUnauthorized
		}
		managedDNSRequest(t, f, actor, http.MethodPost, "", managedDNSCreateBody, status)
		managedDNSRequest(t, f, actor, http.MethodPut, "/"+item.ID, `{"revision":1,"name":"越权更名"}`, status)
		managedDNSRequest(t, f, actor, http.MethodPut, "/"+item.ID+"/status", `{"revision":1,"enabled":false}`, status)
		managedDNSRequest(t, f, actor, http.MethodPut, "/"+item.ID+"/credentials", `{"revision":1,"credentials":{"api_token":"rotated-sensitive-token"}}`, status)
		managedDNSRequest(t, f, actor, http.MethodPost, "/"+item.ID+"/verify", `{"revision":1,"test_domain":"example.test"}`, status)
	}
	if len(secrets.data) != 1 || secrets.getCalls != 0 {
		t.Fatal("越权请求访问了秘密存储")
	}
	for actor, count := range map[string]int{"manager": 1, "viewer": 1, "global-admin": 0, "target": 0, "primary-super-admin": 1} {
		response := managedDNSRequest(t, f, actor, http.MethodGet, "", "", http.StatusOK)
		var page struct {
			Items []dnsAccountView `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != count {
			t.Fatal("独立账户列表未按项目过滤", actor, err)
		}
		if count == 1 && (page.Items[0].OwnerUserID != "manager" || page.Items[0].OwnerState != "assigned" || page.Items[0].OwnerUsername == "" || !page.Items[0].Managed) {
			t.Fatal("列表缺少用户归属或管理标记")
		}
	}
	managedDNSRequest(t, f, "viewer", http.MethodGet, "/"+item.ID, "", http.StatusOK)
	managedDNSRequest(t, f, "global-admin", http.MethodGet, "/"+item.ID, "", http.StatusForbidden)
	managedDNSRequest(t, f, "global-admin", http.MethodGet, "/missing", "", http.StatusForbidden)
	// 系统账户属于别的项目时，不能把本项目独立账户一并隐藏。
	f.server.config.DNS.CredentialRef, f.server.config.DNS.ProjectID = "secret://legacy/hidden", "project-b"
	response := managedDNSRequest(t, f, "manager", http.MethodGet, "", "", http.StatusOK)
	if !strings.Contains(response.Body.String(), item.ID) || strings.Contains(response.Body.String(), "system-dns") || secrets.getCalls != 0 {
		t.Fatal("旧系统账户影响独立账户范围或触发秘密读取")
	}
	if err := f.members.RevokeProjectRole(t.Context(), "primary-super-admin", "project-a", "manager", domain.ProjectRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM users WHERE id='manager'`); err != nil {
		t.Fatal(err)
	}
	f.server.config.DNS.CredentialRef = ""
	response = managedDNSRequest(t, f, "primary-super-admin", http.MethodGet, "", "", http.StatusOK)
	if !strings.Contains(response.Body.String(), `"owner_state":"missing"`) {
		t.Fatal("删除用户后未保留历史归属")
	}
}

func TestManagedDNSAccountRejectsForgedAndStaleVerification(t *testing.T) {
	for _, change := range []string{"rotate", "disable", "revoke"} {
		t.Run(change, func(t *testing.T) {
			f, secrets, item := newManagedDNSFixture(t)
			f.server.dnsAccountVerifier = accountVerifierFunc(func(context.Context, string, string, map[string]string) (dnsverification.Report, error) {
				var err error
				switch change {
				case "rotate":
					_, err = f.members.RotateDNSAccountCredential(t.Context(), "manager", item.ID, 1, "secret://dns/new-version")
				case "disable":
					_, err = f.members.SetDNSAccountEnabled(t.Context(), "manager", item.ID, 1, false)
				case "revoke":
					err = f.members.RevokeProjectRole(t.Context(), "primary-super-admin", "project-a", "manager", domain.ProjectRoleAdmin)
				}
				if err != nil {
					t.Fatal(err)
				}
				return dnsverification.Report{}, nil
			})
			path := "/" + item.ID + "/verify"
			managedDNSRequest(t, f, "manager", http.MethodPost, path, `{"revision":1,"test_domain":"example.test","verified":true}`, http.StatusBadRequest)
			managedDNSRequest(t, f, "manager", http.MethodPost, path, `{"revision":2,"test_domain":"example.test"}`, http.StatusConflict)
			if secrets.getCalls != 0 {
				t.Fatal("伪造或过期请求读取凭据")
			}
			status := http.StatusConflict
			if change == "revoke" {
				status = http.StatusForbidden
			}
			managedDNSRequest(t, f, "manager", http.MethodPost, path, `{"revision":1,"test_domain":"example.test"}`, status)
			stored, err := f.members.GetVisibleDNSAccount(t.Context(), "primary-super-admin", item.ID)
			if err != nil || stored.VerificationState != domain.DNSAccountUnverified {
				t.Fatal("旧验证响应覆盖新状态", err)
			}
		})
	}
}

func TestManagedDNSAccountStrictRequests(t *testing.T) {
	f, secrets, item := newManagedDNSFixture(t)
	for _, field := range []string{`"owner_user_id":"primary-super-admin"`, `"credential_ref":"secret://dns/forged"`, `"verification_state":"verified"`, `"status":"active"`} {
		body := strings.TrimSuffix(managedDNSCreateBody, "}") + "," + field + "}"
		managedDNSRequest(t, f, "manager", http.MethodPost, "", body, http.StatusBadRequest)
	}
	for _, body := range []string{`{"revision":1}`, `{"revision":0,"enabled":true}`, `{"revision":1,"enabled":false,"actor_id":"primary-super-admin"}`} {
		managedDNSRequest(t, f, "manager", http.MethodPut, "/"+item.ID+"/status", body, http.StatusBadRequest)
	}
	managedDNSRequest(t, f, "manager", http.MethodPost, "", strings.Replace(managedDNSCreateBody, "project-a", "project-b", 1), http.StatusForbidden)
	managedDNSRequest(t, f, "manager", http.MethodPost, "", strings.Replace(managedDNSCreateBody, "cloudflare", "unknown", 1), http.StatusBadRequest)
	managedDNSRequest(t, f, "manager", http.MethodPut, "/"+item.ID, `{"revision":1,"name":""}`, http.StatusBadRequest)
	managedDNSRequest(t, f, "manager", http.MethodPut, "/"+item.ID+"/credentials", `{"revision":1,"credentials":{"api_token":""}}`, http.StatusBadRequest)
	if len(secrets.data) != 1 {
		t.Fatal("无效请求留下秘密")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dns/accounts", strings.NewReader(managedDNSCreateBody))
	request.Header.Set("Origin", "https://untrusted.example.test")
	request.AddCookie(f.cookies["manager"])
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || len(secrets.data) != 1 {
		t.Fatal("跨源请求创建了账户", response.Code)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER reject_managed_dns_audit BEFORE INSERT ON audit_logs WHEN NEW.resource_type='dns_account' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	managedDNSRequest(t, f, "manager", http.MethodPut, "/"+item.ID, `{"revision":1,"name":"不能保存"}`, http.StatusServiceUnavailable)
	managedDNSRequest(t, f, "manager", http.MethodPost, "", managedDNSCreateBody, http.StatusServiceUnavailable)
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM dns_accounts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("审计失败留下账户", err)
	}
	stored, err := f.members.GetVisibleDNSAccount(t.Context(), "manager", item.ID)
	if err != nil || stored.Name != item.Name || stored.Revision != item.Revision {
		t.Fatal("审计失败没有回滚改名", err)
	}
	// 不确定提交结果时保留新秘密，避免误删可能已提交的引用；待引用回收流程处理。
	if len(secrets.data) != 2 {
		t.Fatalf("提交错误后的秘密保护不符：%d", len(secrets.data))
	}
}

type failingDNSAccountSecretStore struct {
	*fakeSecretReaderStore
	putErr error
	getErr error
}

func (s failingDNSAccountSecretStore) Put(ctx context.Context, reference string, credentials map[string]string) error {
	if s.putErr != nil {
		return s.putErr
	}
	return s.fakeSecretReaderStore.Put(ctx, reference, credentials)
}

func (s failingDNSAccountSecretStore) Get(ctx context.Context, reference string) (map[string]string, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.fakeSecretReaderStore.Get(ctx, reference)
}

func TestManagedDNSAccountDependencyFailures(t *testing.T) {
	for _, failure := range []string{"missing_store", "write_store", "read_store", "repository", "canceled_verification"} {
		t.Run(failure, func(t *testing.T) {
			f, secrets, item := newManagedDNSFixture(t)
			method, path, body := http.MethodPost, "", managedDNSCreateBody
			status := http.StatusServiceUnavailable
			calls := 0
			f.server.dnsAccountVerifier = accountVerifierFunc(func(context.Context, string, string, map[string]string) (dnsverification.Report, error) {
				calls++
				return dnsverification.Report{}, context.Canceled
			})
			switch failure {
			case "missing_store":
				f.server.secrets = nil
			case "write_store":
				f.server.secrets = failingDNSAccountSecretStore{fakeSecretReaderStore: secrets, putErr: errors.New("internal-sensitive-error")}
			case "read_store":
				f.server.secrets = failingDNSAccountSecretStore{fakeSecretReaderStore: secrets, getErr: errors.New("internal-sensitive-error")}
				path, body = "/"+item.ID+"/verify", `{"revision":1,"test_domain":"example.test"}`
			case "repository":
				f.server.projectMembers = nil
			case "canceled_verification":
				path, body = "/"+item.ID+"/verify", `{"revision":1,"test_domain":"example.test"}`
				status = http.StatusUnprocessableEntity
			}
			managedDNSRequest(t, f, "manager", method, path, body, status)
			stored, err := f.members.GetVisibleDNSAccount(t.Context(), "primary-super-admin", item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "canceled_verification" {
				if calls != 1 || stored.VerificationState != domain.DNSAccountVerificationFailed || stored.Revision != 2 {
					t.Fatal("取消验证没有记录失败")
				}
			} else if calls != 0 || stored.Revision != 1 || len(secrets.data) != 1 {
				t.Fatal("依赖失败仍调用服务商或修改状态")
			}
		})
	}
}
