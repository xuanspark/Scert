package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestDomainInitialDNSAccountAtomicAPI(t *testing.T) {
	f := newProjectMemberAPIFixture(t)
	f.server.projectDomains = f.members
	f.server.repository = repository.NewSQLDomainRepository(f.db)
	account, err := f.members.CreateDNSAccount(t.Context(), "primary-super-admin", domain.DNSAccount{ID: "initial-account", Name: "初始账户", ProjectID: "default", Provider: "cloudflare", CredentialRef: "secret://dns/initial"})
	if err != nil {
		t.Fatal(err)
	}
	account, err = f.members.RecordDNSAccountVerification(t.Context(), "primary-super-admin", account.ID, account.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, actor, provider, challenge, accountID string
		revision                                    int64
		status                                      int
	}{
		{"success", "primary-super-admin", "cloudflare", "dns-01", account.ID, account.Revision, http.StatusCreated},
		{"old-version", "primary-super-admin", "cloudflare", "dns-01", account.ID, 1, http.StatusConflict},
		{"wrong-provider", "primary-super-admin", "route53", "dns-01", account.ID, account.Revision, http.StatusUnprocessableEntity},
		{"wrong-challenge", "primary-super-admin", "cloudflare", "http-01", account.ID, account.Revision, http.StatusBadRequest},
		{"no-account", "primary-super-admin", "cloudflare", "dns-01", "", 2, http.StatusBadRequest},
		{"no-permission", "viewer", "cloudflare", "dns-01", account.ID, account.Revision, http.StatusForbidden},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			name := scenario.name + ".example.test"
			body := fmt.Sprintf(`{"name":%q,"certificate_type":"single","challenge_type":%q,"dns_provider":%q,"dns_account_id":%q,"dns_account_revision":%d}`, name, scenario.challenge, scenario.provider, scenario.accountID, scenario.revision)
			response := serveUserRequest(f.server, f.cookies[scenario.actor], http.MethodPost, "/api/v1/domains", body)
			if response.Code != scenario.status {
				t.Fatal("创建接口状态错误", response.Code, response.Body.String())
			}
			if response.Code == http.StatusCreated {
				var item domain.Domain
				if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
					t.Fatal(err)
				}
				loaded, err := repository.NewSQLDomainRepository(f.db).GetByID(t.Context(), item.ID)
				if err != nil || item.DNSBindingID == "" || item.DNSBindingID != loaded.DNSBindingID || loaded.DNSAccountID != account.ID {
					t.Fatal("创建响应与原子绑定不符", err)
				}
				response = serveUserRequest(f.server, f.cookies[scenario.actor], http.MethodPut, "/api/v1/domains/"+item.ID, body)
				if response.Code != http.StatusBadRequest {
					t.Fatal("普通更新不能绕过绑定令牌", response.Code)
				}
			} else {
				var count int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domains WHERE name = ?`, name).Scan(&count); err != nil || count != 0 {
					t.Fatal("失败后遗留未绑定域名", err)
				}
			}
		})
	}
}
