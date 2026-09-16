package dnsprovider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAliyunAuthorizationRecordAPIUsesSingleAttemptWithoutRetry(t *testing.T) {
	api, err := newAliyunAuthorizationRecordAPI(scopeCredentials())
	if err != nil {
		t.Fatalf("创建阿里云权限探测客户端失败：%v", err)
	}
	records, ok := api.(aliyunRecords)
	if !ok || records.client == nil || records.client.RetryOptions == nil {
		t.Fatalf("权限探测必须使用带显式重试策略的专用客户端：%T", api)
	}
	if records.client.RetryOptions.Retryable || records.client.RetryOptions.MaxAttempts != 1 {
		t.Fatalf("权限探测客户端不得自动重试：%+v", records.client.RetryOptions)
	}
}

func TestAliyunACMERecordAPIDoesNotUseAuthorizationRetryPolicy(t *testing.T) {
	api, err := newAliyunRecordAPI(scopeCredentials())
	if err != nil {
		t.Fatalf("创建阿里云 ACME 客户端失败：%v", err)
	}
	records, ok := api.(aliyunRecords)
	if !ok || records.client == nil {
		t.Fatalf("ACME 客户端类型错误：%T", api)
	}
	if records.client.RetryOptions != nil {
		t.Fatalf("ACME 客户端不应复用权限探测专用重试策略：%+v", records.client.RetryOptions)
	}
}

func TestAliyunAuthorizationCheckerRechecksEveryRequestAndDetectsRevocation(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	calls := 0
	revoked := false
	checker := newAliyunAuthorizationCheckerForTest()
	checker.now = func() time.Time { return now }
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		calls++
		if revoked {
			return errors.New("Forbidden.RAM")
		}
		return nil
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
		"managed_zones":     "example.com",
		"allowed_domains":   "example.com,www.example.com",
	}

	if err := checker.VerifyDomain(context.Background(), Aliyun, "www.example.com", credentials); err != nil {
		t.Fatalf("首次授权检查失败：%v", err)
	}
	if err := checker.VerifyDomain(context.Background(), Aliyun, "www.example.com", credentials); err != nil {
		t.Fatalf("缓存授权检查失败：%v", err)
	}
	if calls != 2 {
		t.Fatalf("每次操作必须重新验证权限：calls=%d", calls)
	}

	revoked = true
	if err := checker.VerifyDomain(context.Background(), Aliyun, "www.example.com", credentials); err == nil {
		t.Fatal("撤权后不得复用之前的成功结果")
	}
	if calls != 3 {
		t.Fatalf("撤权后未重新探测：calls=%d", calls)
	}
}

func TestAliyunAuthorizationCheckerInvalidatesAfterPermissionFailure(t *testing.T) {
	calls := 0
	checker := newAliyunAuthorizationCheckerForTest()
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		calls++
		return nil
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
		"managed_zones":     "example.com",
		"allowed_domains":   "example.com",
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
	checker := newAliyunAuthorizationCheckerForTest()
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		calls++
		return errors.New("权限不足")
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
		"managed_zones":     "example.com",
		"allowed_domains":   "example.com",
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
	checker := newAliyunAuthorizationCheckerForTest()
	checker.verifyDomainFunc = func(context.Context, string, map[string]string) error {
		return nil
	}
	credentials := map[string]string{
		"access_key_id":     "test-id",
		"access_key_secret": "test-secret",
		"managed_zones":     "example.com",
		"allowed_domains":   "example.com",
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
			expected: "当前阿里云 RAM 账号未获得域名 example.com 所需的 DNS 操作权限。请管理员核对实际托管区资源 acs:alidns:*:*:domain/example.com，按失败操作补充必要的读取、新增或删除权限；不要直接扩大为全部操作或全部域名权限",
		},
		{
			err:      errors.New("SDKError: Code: Forbidden.NoPermission, Message: Specified access key is not authorized."),
			domain:   "solit.cn",
			expected: "当前阿里云 RAM 账号未获得域名 solit.cn 所需的 DNS 操作权限。请管理员核对实际托管区资源 acs:alidns:*:*:domain/solit.cn，按失败操作补充必要的读取、新增或删除权限；不要直接扩大为全部操作或全部域名权限",
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
			err:      errors.New("无法识别域名 www.example.com 的权威 DNS 托管区，已停止验证：SOA lookup failed"),
			domain:   "www.example.com",
			expected: "未能从公开 DNS 的 SOA 记录识别该域名唯一的权威托管区，系统未构造阿里云客户端，也未创建临时 TXT 记录。请检查域名是否已正确委派并可被公网 DNS 查询后重试",
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
