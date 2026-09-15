package dnsprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func scopeCredentials() map[string]string {
	return map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
	}
}

func TestResolveAliyunTargetDomainUsesOnlyRegistrableRoot(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "www.sub.example.com", want: "example.com"},
		{name: "WWW.EXAMPLE.COM.", want: "example.com"},
		{name: "*.example.com", want: "example.com"},
		{name: "example.co.uk", want: "example.co.uk"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveAliyunTargetDomain(test.name)
			if err != nil || got != test.want {
				t.Fatalf("ResolveAliyunTargetDomain(%q) = %q, %v; want %q", test.name, got, err, test.want)
			}
		})
	}
}

func TestResolveAliyunTargetDomainRejectsProtectedAndInvalidDomains(t *testing.T) {
	for _, name := range []string{"pcux.cn", "*.pcux.cn", "api.pcux.cn", "com", "*.com", ""} {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveAliyunTargetDomain(name); err == nil {
				t.Fatalf("ResolveAliyunTargetDomain(%q) unexpectedly succeeded", name)
			}
		})
	}
}

type fakeAliyunRecords struct {
	zones, rrs, values, described, deleted []string
	addErr, describeErr, deleteErr         error
	describeInfo                           aliyunRecordInfo
	canceledCleanup                        bool
}

func (f *fakeAliyunRecords) Add(_ context.Context, zone, rr, value string) (string, error) {
	f.zones = append(f.zones, zone)
	f.rrs = append(f.rrs, rr)
	f.values = append(f.values, value)
	if f.addErr != nil {
		return "", f.addErr
	}
	return "created-record-id", nil
}

func (f *fakeAliyunRecords) Describe(_ context.Context, id string) (aliyunRecordInfo, error) {
	f.described = append(f.described, id)
	if f.describeErr != nil {
		return aliyunRecordInfo{}, f.describeErr
	}
	if f.describeInfo.RecordID != "" {
		return f.describeInfo, nil
	}
	if len(f.zones) == 0 || len(f.rrs) == 0 || len(f.values) == 0 {
		return aliyunRecordInfo{}, errors.New("no temporary record exists")
	}
	return aliyunRecordInfo{
		RecordID:   id,
		DomainName: f.zones[len(f.zones)-1],
		RR:         f.rrs[len(f.rrs)-1],
		Type:       "TXT",
		Value:      f.values[len(f.values)-1],
	}, nil
}

func (f *fakeAliyunRecords) Delete(ctx context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	f.canceledCleanup = ctx.Err() != nil
	return f.deleteErr
}

func TestAliyunProbeUsesTheSingleRegistrableRootWithoutFallback(t *testing.T) {
	api := &fakeAliyunRecords{addErr: errors.New("Forbidden.RAM")}
	checker := NewAliyunAuthorizationChecker()
	constructed := 0
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return api, nil
	}

	if _, err := checker.Verify(t.Context(), "www.sub.example.com", scopeCredentials()); err == nil {
		t.Fatal("RAM denial unexpectedly succeeded")
	}
	if constructed != 1 || len(api.zones) != 1 || api.zones[0] != "example.com" {
		t.Fatalf("probe did not use only the registrable root: constructed=%d zones=%v", constructed, api.zones)
	}
	if len(api.deleted) != 0 {
		t.Fatalf("failed Add must not guess a record ID to delete: %v", api.deleted)
	}
}

func TestAliyunProtectedDomainDoesNotConstructClientOrCallCloud(t *testing.T) {
	api := &fakeAliyunRecords{}
	checker := NewAliyunAuthorizationChecker()
	constructed := 0
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return api, nil
	}

	for _, name := range []string{"pcux.cn", "*.pcux.cn", "api.pcux.cn"} {
		if _, err := checker.Verify(t.Context(), name, scopeCredentials()); err == nil {
			t.Fatalf("protected domain %q unexpectedly succeeded", name)
		}
	}
	if constructed != 0 || len(api.zones) != 0 || len(api.described) != 0 || len(api.deleted) != 0 {
		t.Fatalf("protected domain touched cloud state: constructed=%d zones=%v described=%v deleted=%v", constructed, api.zones, api.described, api.deleted)
	}
}

func TestAliyunProviderDefersClientCreationAndPinsCleanupIdentity(t *testing.T) {
	provider, err := newScopedAliyunProvider(scopeCredentials())
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAliyunRecords{}
	constructed := 0
	provider.newAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return api, nil
	}
	provider.challengeInfo = func(_ context.Context, name, _ string) (string, string, string) {
		fqdn := "_acme-challenge." + name + "."
		return fqdn, fqdn, "txt-value"
	}

	if constructed != 0 {
		t.Fatal("provider construction must not construct a cloud client")
	}
	if err := provider.Present(t.Context(), "www.sub.example.com", "token", "key"); err != nil {
		t.Fatal(err)
	}
	if constructed != 1 || len(api.zones) != 1 || api.zones[0] != "example.com" || api.rrs[0] != "_acme-challenge.www.sub" {
		t.Fatalf("provider wrote outside its single registrable root: constructed=%d zones=%v rrs=%v", constructed, api.zones, api.rrs)
	}
	if err := provider.Present(t.Context(), "www.sub.example.com", "token", "key"); err == nil {
		t.Fatal("duplicate challenge unexpectedly overwrote a pending record")
	}
	if err := provider.CleanUp(t.Context(), "www.sub.example.com", "other-token", "key"); err != nil {
		t.Fatal(err)
	}
	if len(api.deleted) != 0 {
		t.Fatalf("cleanup for a different challenge deleted a record: %v", api.deleted)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := provider.CleanUp(ctx, "www.sub.example.com", "token", "key"); err != nil {
		t.Fatal(err)
	}
	if len(api.deleted) != 1 || api.deleted[0] != "created-record-id" || api.canceledCleanup {
		t.Fatalf("cleanup did not use the exact created record ID: deleted=%v canceled=%v", api.deleted, api.canceledCleanup)
	}
}

func TestAliyunProviderRejectsCNAMEBeforeClientConstruction(t *testing.T) {
	provider, err := newScopedAliyunProvider(scopeCredentials())
	if err != nil {
		t.Fatal(err)
	}
	constructed := 0
	provider.newAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return &fakeAliyunRecords{}, nil
	}
	provider.challengeInfo = func(context.Context, string, string) (string, string, string) {
		return "_acme-challenge.www.sub.example.com.", "_acme-challenge.delegated.example.net.", "txt-value"
	}

	if err := provider.Present(t.Context(), "www.sub.example.com", "token", "key"); err == nil || !strings.Contains(err.Error(), "CNAME") {
		t.Fatalf("CNAME redirection was not rejected: %v", err)
	}
	if constructed != 0 {
		t.Fatalf("CNAME redirection constructed a cloud client: %d", constructed)
	}
}
