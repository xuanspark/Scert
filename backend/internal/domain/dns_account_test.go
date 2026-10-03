package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDNSAccountValidationAndSerialization(t *testing.T) {
	base := DNSAccount{ID: "dns-one", Name: "生产 DNS", ProjectID: "default", OwnerUserID: "primary-super-admin",
		Provider: "aliyun", CredentialRef: "secret://dns/aliyun/example", Status: DNSAccountActive,
		VerificationState: DNSAccountUnverified, Revision: 1}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*DNSAccount){
		"empty_id":             func(a *DNSAccount) { a.ID = "" },
		"bad_project":          func(a *DNSAccount) { a.ProjectID = "../other" },
		"missing_owner":        func(a *DNSAccount) { a.OwnerUserID = "" },
		"blank_name":           func(a *DNSAccount) { a.Name = "  " },
		"control_name":         func(a *DNSAccount) { a.Name = "账户\n名称" },
		"long_name":            func(a *DNSAccount) { a.Name = strings.Repeat("中", 129) },
		"invalid_provider":     func(a *DNSAccount) { a.Provider = "other/provider" },
		"wrong_secret_type":    func(a *DNSAccount) { a.CredentialRef = "secret://ssh/password" },
		"empty_secret_id":      func(a *DNSAccount) { a.CredentialRef = "secret://dns/" },
		"secret_whitespace":    func(a *DNSAccount) { a.CredentialRef += "\n" },
		"invalid_state":        func(a *DNSAccount) { a.Status = "deleted" },
		"invalid_verification": func(a *DNSAccount) { a.VerificationState = "trusted" },
		"zero_revision":        func(a *DNSAccount) { a.Revision = 0 },
		"overflow_revision":    func(a *DNSAccount) { a.Revision = 2147483648 },
	} {
		t.Run(name, func(t *testing.T) {
			item := base
			change(&item)
			if item.Validate() == nil {
				t.Fatal("无效账户未被拒绝")
			}
		})
	}
	payload, err := json.Marshal(base)
	if err != nil || strings.Contains(string(payload), "secret://") || strings.Contains(string(payload), "credential_ref") {
		t.Fatal("账户 JSON 暴露秘密引用", err)
	}
}
