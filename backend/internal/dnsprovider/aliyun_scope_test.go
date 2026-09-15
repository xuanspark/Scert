package dnsprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func scopeCredentials() map[string]string {
	return map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret", "managed_zones": "example.com,sub.example.com", "allowed_domains": "www.sub.example.com,*.example.com"}
}

func TestAliyunScopeBoundaries(t *testing.T) {
	scope, err := ParseAliyunScope(scopeCredentials())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, zone string }{
		{"www.sub.example.com", "sub.example.com"},
		{"WWW.EXAMPLE.COM.", "example.com"},
		{"*.example.com", "example.com"},
		{"example.com", ""},
		{"evil-example.com", ""},
		{"example.com.evil.net", ""},
		{"deep.www.example.com", ""},
		{"*.sub.example.com", ""},
		{"pcux.cn", ""},
		{"com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zone, err := scope.Resolve(tc.name)
			if zone != tc.zone || (err != nil) != (tc.zone == "") {
				t.Fatalf("范围校验错误：zone=%s err=%v", zone, err)
			}
		})
	}
	for _, value := range []string{"", "com", "co.uk", "*.example.com", "example.com,", "example.com,co.uk"} {
		credentials := scopeCredentials()
		credentials["managed_zones"] = value
		if _, err := ParseAliyunScope(credentials); err == nil {
			t.Fatalf("不安全托管区未拒绝：%s", value)
		}
	}
	credentials := scopeCredentials()
	credentials["allowed_domains"] = "outside.net"
	if _, err := ParseAliyunScope(credentials); err == nil {
		t.Fatal("越界的允许范围必须整体拒绝")
	}
}

type fakeAliyunRecords struct {
	zones, rrs, deleted []string
	addErr, deleteErr   error
	canceledCleanup     bool
}

func (f *fakeAliyunRecords) Add(_ context.Context, zone, rr, _ string) (string, error) {
	f.zones = append(f.zones, zone)
	f.rrs = append(f.rrs, rr)
	return "created-record-id", f.addErr
}

func (f *fakeAliyunRecords) Delete(ctx context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	f.canceledCleanup = ctx.Err() != nil
	return f.deleteErr
}

func TestAliyunProbeNeverFallsBackOrCallsCloudOutsideScope(t *testing.T) {
	api := &fakeAliyunRecords{addErr: errors.New("Forbidden.RAM")}
	checker := NewAliyunAuthorizationChecker()
	constructed := 0
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) { constructed++; return api, nil }
	credentials := scopeCredentials()
	for _, name := range []string{"example.com", "outside.net", "pcux.cn", "*.sub.example.com"} {
		if _, err := checker.Verify(t.Context(), name, credentials); err == nil {
			t.Fatal("越界请求未拒绝")
		}
	}
	if constructed != 0 {
		t.Fatal("越界请求不能创建云客户端")
	}
	if _, err := checker.Verify(t.Context(), "www.sub.example.com", credentials); err == nil {
		t.Fatal("RAM 拒绝必须返回失败")
	}
	if len(api.zones) != 1 || api.zones[0] != "sub.example.com" || !strings.HasSuffix(api.rrs[0], ".www") {
		t.Fatalf("必须仅写授权子域：%v %v", api.zones, api.rrs)
	}
	if len(api.deleted) != 0 {
		t.Fatal("新增失败不能猜测记录 ID 删除")
	}
	api.addErr = nil
	api.deleteErr = errors.New("Forbidden.RAM")
	report, err := checker.Verify(t.Context(), "www.sub.example.com", credentials)
	if err == nil || report.Verified || !report.Domains[0].Writable || report.Domains[0].Cleanup {
		t.Fatal("清理失败不能证明权限通过")
	}
	api.deleteErr = nil
	report, err = checker.Verify(t.Context(), "www.sub.example.com", credentials)
	if err != nil || !report.Verified || api.deleted[len(api.deleted)-1] != "created-record-id" {
		t.Fatal("必须删除本次新增记录")
	}
}

func TestAliyunProviderPinsZoneAndCleanupIdentity(t *testing.T) {
	scope, _ := ParseAliyunScope(scopeCredentials())
	api := &fakeAliyunRecords{}
	p := &scopedAliyunProvider{scope: scope, api: api, records: make(map[[32]byte]string), challengeInfo: func(_ context.Context, name, _ string) (string, string, string) {
		fqdn := "_acme-challenge." + name + "."
		return fqdn, fqdn, "txt-value"
	}}
	if err := p.Present(t.Context(), "outside.net", "token", "key"); err == nil || len(api.zones) != 0 {
		t.Fatal("实际 Provider 必须零调用拒绝越界")
	}
	if err := p.Present(t.Context(), "www.sub.example.com", "token", "key"); err != nil {
		t.Fatal(err)
	}
	if len(api.zones) != 1 || api.zones[0] != "sub.example.com" || api.rrs[0] != "_acme-challenge.www" {
		t.Fatal("实际写入没有锁定指定托管区")
	}
	if err := p.Present(t.Context(), "www.sub.example.com", "token", "key"); err == nil {
		t.Fatal("重复挑战不能覆盖待清理 ID")
	}
	_ = p.CleanUp(t.Context(), "www.sub.example.com", "other", "key")
	if len(api.deleted) != 0 {
		t.Fatal("不能清理其他挑战")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.CleanUp(ctx, "www.sub.example.com", "token", "key"); err != nil || api.canceledCleanup || len(api.deleted) != 1 {
		t.Fatal("请求取消后仍需有界清理本次 ID")
	}
	p.challengeInfo = func(context.Context, string, string) (string, string, string) {
		return "_acme-challenge.www.sub.example.com.", "_acme-challenge.outside.net.", "value"
	}
	if err := p.Present(t.Context(), "www.sub.example.com", "new", "key"); err == nil || len(api.zones) != 1 {
		t.Fatal("CNAME 越界不能调用云 API")
	}
}
