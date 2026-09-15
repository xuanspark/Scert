package dnsprovider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuthorizationZoneCandidatesPreferSpecificZones(t *testing.T) {
	got := authorizationZoneCandidates("*.www.example.com.")
	want := []string{"www.example.com", "example.com"}
	if len(got) != len(want) {
		t.Fatalf("候选托管区数量错误：got=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("候选托管区顺序错误：got=%v want=%v", got, want)
		}
	}
}

func TestAliyunAuthorizationCheckerCachesSuccessfulAuthorization(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	calls := 0
	checker := NewAliyunAuthorizationChecker()
	checker.now = func() time.Time { return now }
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		calls++
		return nil
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
	}

	if err := checker.VerifyDomain(context.Background(), Aliyun, "www.example.com", credentials); err != nil {
		t.Fatalf("首次授权检查失败：%v", err)
	}
	if err := checker.VerifyDomain(context.Background(), Aliyun, "www.example.com", credentials); err != nil {
		t.Fatalf("缓存授权检查失败：%v", err)
	}
	if calls != 1 {
		t.Fatalf("短时授权缓存未生效：calls=%d", calls)
	}

	now = now.Add(aliyunAuthorizationCacheTTL + time.Second)
	if err := checker.VerifyDomain(context.Background(), Aliyun, "www.example.com", credentials); err != nil {
		t.Fatalf("缓存过期后的授权检查失败：%v", err)
	}
	if calls != 2 {
		t.Fatalf("授权缓存过期后未重新探测：calls=%d", calls)
	}
}

func TestAliyunAuthorizationCheckerInvalidatesAfterPermissionFailure(t *testing.T) {
	calls := 0
	checker := NewAliyunAuthorizationChecker()
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		calls++
		return nil
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
	}

	if err := checker.VerifyDomain(context.Background(), Aliyun, "example.com", credentials); err != nil {
		t.Fatalf("首次授权检查失败：%v", err)
	}
	checker.InvalidateDomain(Aliyun, "example.com", credentials)
	if err := checker.VerifyDomain(context.Background(), Aliyun, "example.com", credentials); err != nil {
		t.Fatalf("权限失败后的重新授权检查失败：%v", err)
	}
	if calls != 2 {
		t.Fatalf("授权缓存未在权限失败后失效：calls=%d", calls)
	}
}

func TestAliyunAuthorizationCheckerDoesNotCacheFailedAuthorization(t *testing.T) {
	calls := 0
	checker := NewAliyunAuthorizationChecker()
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		calls++
		return errors.New("权限不足")
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
	}

	if err := checker.VerifyDomain(context.Background(), Aliyun, "example.com", credentials); err == nil {
		t.Fatal("权限失败时应返回错误")
	}
	if err := checker.VerifyDomain(context.Background(), Aliyun, "example.com", credentials); err == nil {
		t.Fatal("权限失败不应被缓存为成功")
	}
	if calls != 2 {
		t.Fatalf("失败授权检查被错误缓存：calls=%d", calls)
	}
}

func TestAliyunAuthorizationCheckerCacheIsSafeForConcurrentUse(t *testing.T) {
	checker := NewAliyunAuthorizationChecker()
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		return nil
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
	}
	done := make(chan struct{})
	for index := 0; index < 20; index++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = checker.VerifyDomain(context.Background(), Aliyun, "example.com", credentials)
			checker.InvalidateDomain(Aliyun, "example.com", credentials)
		}()
	}
	for index := 0; index < 20; index++ {
		<-done
	}
}

func TestDiagnoseAliyunError(t *testing.T) {
	testCases := []struct {
		err      error
		domain   string
		expected string
	}{
		{
			err:      errors.New("SDKError: Code: Forbidden.RAM, Message: User not authorized to operate on the specified resource."),
			domain:   "example.com",
			expected: "当前阿里云 RAM 账号未获得域名 example.com 的 DNS 解析权限。请在阿里云 RAM 控制台授予该账号针对 acs:alidns:*:*:domain/example.com 的操作权限（Action: alidns:*）",
		},
		{
			err:      errors.New("SDKError: Code: Forbidden.NoPermission, Message: Specified access key is not authorized."),
			domain:   "solit.cn",
			expected: "当前阿里云 RAM 账号未获得域名 solit.cn 的 DNS 解析权限。请在阿里云 RAM 控制台授予该账号针对 acs:alidns:*:*:domain/solit.cn 的操作权限（Action: alidns:*）",
		},
		{
			err:      errors.New("SDKError: Code: NoSuchDomain, Message: The specified domain name does not exist."),
			domain:   "nonexistent.cn",
			expected: "域名 nonexistent.cn 未在当前阿里云账号下添加 DNS 托管，请先在阿里云云解析 DNS 控制台添加此域名",
		},
		{
			err:      errors.New("SDKError: Code: InvalidAccessKeyId.NotFound, Message: Specified access key is not found."),
			domain:   "solit.cn",
			expected: "阿里云 AccessKey ID 或 AccessKey Secret 无效，请检查系统 DNS 凭据配置",
		},
		{
			err:      nil,
			domain:   "solit.cn",
			expected: "",
		},
	}

	for _, tc := range testCases {
		got := DiagnoseAliyunError(tc.err, tc.domain)
		if got != tc.expected {
			t.Errorf("DiagnoseAliyunError(%v, %s) = %q; want %q", tc.err, tc.domain, got, tc.expected)
		}
	}
}
