package dnsprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func scopeCredentials() map[string]string {
	return map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
	}
}

func staticAliyunZoneResolver(zone string) AliyunZoneResolver {
	return aliyunZoneResolverFunc(func(context.Context, string) (string, error) {
		return zone, nil
	})
}

func newAliyunAuthorizationCheckerForTest() *AliyunAuthorizationChecker {
	checker := NewAliyunAuthorizationChecker()
	checker.zoneResolver = staticAliyunZoneResolver("example.com")
	return checker
}

func TestResolveAliyunTargetDomainUsesOnlyAuthoritativeZone(t *testing.T) {
	resolverCalls := make([]string, 0, 1)
	resolver := aliyunZoneResolverFunc(func(_ context.Context, requested string) (string, error) {
		resolverCalls = append(resolverCalls, requested)
		if requested != "www.sub.example.com" {
			t.Fatalf("托管区识别收到了错误域名：%s", requested)
		}
		return "sub.example.com.", nil
	})

	got, err := resolveAliyunTargetDomain(t.Context(), "www.sub.example.com", resolver)
	if err != nil || got != "sub.example.com" {
		t.Fatalf("权威托管区识别结果错误：got=%q err=%v", got, err)
	}
	if len(resolverCalls) != 1 {
		t.Fatalf("不应枚举候选父域：resolverCalls=%v", resolverCalls)
	}
}

func TestAuthoritativeAliyunZoneResolverUsesFQDNAndBoundedDeadline(t *testing.T) {
	const timeout = 80 * time.Millisecond
	called := false
	resolver := authoritativeAliyunZoneResolver{
		timeout: timeout,
		findZone: func(ctx context.Context, fqdn string) (string, error) {
			called = true
			if fqdn != "www.sub.example.com." {
				t.Fatalf("权威托管区查询必须使用完整域名：%q", fqdn)
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("权威托管区查询必须具有截止时间")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > timeout {
				t.Fatalf("权威托管区查询截止时间未受限：%s", remaining)
			}
			return "sub.example.com.", nil
		},
	}

	zone, err := resolver.Resolve(t.Context(), "WWW.Sub.Example.Com.")
	if err != nil || zone != "sub.example.com" {
		t.Fatalf("权威托管区解析结果错误：zone=%q err=%v", zone, err)
	}
	if !called {
		t.Fatal("未执行权威托管区查询")
	}
}

func TestResolveAliyunTargetDomainRejectsProtectedAndInvalidDomainsBeforeResolver(t *testing.T) {
	resolverCalls := 0
	resolver := aliyunZoneResolverFunc(func(context.Context, string) (string, error) {
		resolverCalls++
		return "example.com", nil
	})
	for _, name := range []string{"pcux.cn", "*.pcux.cn", "api.pcux.cn", "com", "*.com", ""} {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveAliyunTargetDomain(t.Context(), name, resolver); err == nil {
				t.Fatalf("resolveAliyunTargetDomain(%q) unexpectedly succeeded", name)
			}
		})
	}
	if resolverCalls != 0 {
		t.Fatalf("受保护或无效域名不应发起 SOA 查询：%d", resolverCalls)
	}
}

func TestResolveAliyunTargetDomainFailsClosedWhenAuthoritativeZoneCannotBeIdentified(t *testing.T) {
	resolver := aliyunZoneResolverFunc(func(context.Context, string) (string, error) {
		return "", errors.New("SOA lookup failed")
	})
	if _, err := resolveAliyunTargetDomain(t.Context(), "www.example.com", resolver); err == nil || !strings.Contains(err.Error(), "SOA lookup failed") {
		t.Fatalf("权威托管区识别失败必须阻止后续操作：%v", err)
	}
}

func TestResolveAliyunTargetDomainRejectsUnrelatedAuthoritativeZone(t *testing.T) {
	resolver := staticAliyunZoneResolver("unrelated.example.net")
	if _, err := resolveAliyunTargetDomain(t.Context(), "www.example.com", resolver); err == nil || !strings.Contains(err.Error(), "不属于") {
		t.Fatalf("无关的权威托管区必须失败关闭：%v", err)
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

func (f *fakeAliyunRecords) Describe(_ context.Context, zone, rr, id string) (aliyunRecordInfo, error) {
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

func TestAliyunProbeUsesTheSingleAuthoritativeZoneWithoutFallback(t *testing.T) {
	api := &fakeAliyunRecords{addErr: errors.New("Forbidden.RAM")}
	checker := newAliyunAuthorizationCheckerForTest()
	checker.zoneResolver = staticAliyunZoneResolver("sub.example.com")
	constructed := 0
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return api, nil
	}

	if _, err := checker.Verify(t.Context(), "www.sub.example.com", scopeCredentials()); err == nil {
		t.Fatal("RAM denial unexpectedly succeeded")
	}
	if constructed != 1 || len(api.zones) != 1 || api.zones[0] != "sub.example.com" {
		t.Fatalf("探测未严格使用唯一权威托管区：constructed=%d zones=%v", constructed, api.zones)
	}
	if len(api.deleted) != 0 {
		t.Fatalf("failed Add must not guess a record ID to delete: %v", api.deleted)
	}
}

func TestAliyunProtectedDomainDoesNotConstructClientOrCallCloud(t *testing.T) {
	api := &fakeAliyunRecords{}
	checker := newAliyunAuthorizationCheckerForTest()
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

func TestAliyunAuthorizationDoesNotConstructClientWhenZoneResolutionFails(t *testing.T) {
	api := &fakeAliyunRecords{}
	checker := newAliyunAuthorizationCheckerForTest()
	checker.zoneResolver = aliyunZoneResolverFunc(func(context.Context, string) (string, error) {
		return "", errors.New("SOA lookup failed")
	})
	constructed := 0
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return api, nil
	}

	if _, err := checker.Verify(t.Context(), "www.example.com", scopeCredentials()); err == nil {
		t.Fatal("权威托管区识别失败时不应继续探测")
	}
	if constructed != 0 || len(api.zones) != 0 || len(api.described) != 0 || len(api.deleted) != 0 {
		t.Fatalf("权威托管区识别失败时触碰了云端：constructed=%d zones=%v described=%v deleted=%v", constructed, api.zones, api.described, api.deleted)
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
	provider.zoneResolver = staticAliyunZoneResolver("sub.example.com")
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
	if constructed != 1 || len(api.zones) != 1 || api.zones[0] != "sub.example.com" || api.rrs[0] != "_acme-challenge.www" {
		t.Fatalf("provider 写入位置不属于唯一权威托管区：constructed=%d zones=%v rrs=%v", constructed, api.zones, api.rrs)
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
	provider.zoneResolver = staticAliyunZoneResolver("example.com")
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

func TestAliyunProviderDoesNotConstructClientWhenZoneResolutionFails(t *testing.T) {
	provider, err := newScopedAliyunProvider(scopeCredentials())
	if err != nil {
		t.Fatal(err)
	}
	provider.zoneResolver = aliyunZoneResolverFunc(func(context.Context, string) (string, error) {
		return "", errors.New("SOA lookup failed")
	})
	constructed := 0
	provider.newAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return &fakeAliyunRecords{}, nil
	}

	if err := provider.Present(t.Context(), "www.example.com", "token", "key"); err == nil {
		t.Fatal("权威托管区识别失败时不应继续写入")
	}
	if constructed != 0 {
		t.Fatalf("权威托管区识别失败时不应构造阿里云客户端：%d", constructed)
	}
}
