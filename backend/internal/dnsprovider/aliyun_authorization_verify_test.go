package dnsprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type authorizationCall struct {
	name string
	id   string
}

type authorizationRecordAPI struct {
	calls              []authorizationCall
	zone               string
	rr                 string
	value              string
	addErr             error
	describeErr        error
	deleteErr          error
	describeInfo       aliyunRecordInfo
	cancelAfterAdd     context.CancelFunc
	cleanupWasCanceled bool
	callTimeouts       []time.Duration
}

func (a *authorizationRecordAPI) Add(ctx context.Context, zone, rr, value string) (string, error) {
	a.recordCall("add", "", ctx)
	a.zone, a.rr, a.value = zone, rr, value
	if a.cancelAfterAdd != nil {
		a.cancelAfterAdd()
	}
	if a.addErr != nil {
		return "", a.addErr
	}
	return "authorization-record-id", nil
}

func (a *authorizationRecordAPI) Describe(ctx context.Context, recordID string) (aliyunRecordInfo, error) {
	a.recordCall("describe", recordID, ctx)
	if a.describeErr != nil {
		return aliyunRecordInfo{}, a.describeErr
	}
	if err := ctx.Err(); err != nil {
		return aliyunRecordInfo{}, err
	}
	if a.describeInfo.RecordID != "" {
		return a.describeInfo, nil
	}
	return aliyunRecordInfo{
		RecordID:   recordID,
		DomainName: a.zone,
		RR:         a.rr,
		Type:       "TXT",
		Value:      a.value,
	}, nil
}

func (a *authorizationRecordAPI) Delete(ctx context.Context, recordID string) error {
	a.recordCall("delete", recordID, ctx)
	a.cleanupWasCanceled = ctx.Err() != nil
	return a.deleteErr
}

func (a *authorizationRecordAPI) recordCall(name, id string, ctx context.Context) {
	a.calls = append(a.calls, authorizationCall{name: name, id: id})
	timeout := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	a.callTimeouts = append(a.callTimeouts, timeout)
}

func newAuthorizationCheckerForTest(api aliyunRecordAPI) *AliyunAuthorizationChecker {
	checker := NewAliyunAuthorizationChecker()
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) {
		return api, nil
	}
	return checker
}

func TestAliyunAuthorizationVerifyClosesWriteReadCleanupLoop(t *testing.T) {
	api := &authorizationRecordAPI{}
	report, err := newAuthorizationCheckerForTest(api).Verify(t.Context(), "www.sub.example.com", scopeCredentials())
	if err != nil {
		t.Fatalf("阿里云授权闭环验证失败：%v", err)
	}
	if !report.Verified || report.RecordName == "" || len(report.Domains) != 1 {
		t.Fatalf("授权报告不完整：%+v", report)
	}
	domain := report.Domains[0]
	if !domain.Writable || !domain.Readable || !domain.Cleanup || domain.Status != "可操作" {
		t.Fatalf("授权闭环状态不正确：%+v", domain)
	}
	if got := callNames(api.calls); got != "add,describe,delete" {
		t.Fatalf("API 调用顺序错误：%s", got)
	}
	if api.calls[1].id != "authorization-record-id" || api.calls[2].id != "authorization-record-id" {
		t.Fatalf("查询或清理未使用本次创建的记录 ID：%+v", api.calls)
	}
	if len(api.callTimeouts) != 3 {
		t.Fatalf("未记录全部 API 调用时限：%+v", api.callTimeouts)
	}
	for index, timeout := range api.callTimeouts {
		if timeout <= 0 || timeout > aliyunAPICallTimeout {
			t.Fatalf("第 %d 次阿里云 API 调用未限制在 %s 内：%s", index+1, aliyunAPICallTimeout, timeout)
		}
	}
}

func TestAliyunAuthorizationVerifyDoesNotCallCloudOutsideExplicitScope(t *testing.T) {
	api := &authorizationRecordAPI{}
	checker := newAuthorizationCheckerForTest(api)
	constructed := 0
	checker.recordAPI = func(map[string]string) (aliyunRecordAPI, error) {
		constructed++
		return api, nil
	}
	report, err := checker.Verify(t.Context(), "pcux.cn", scopeCredentials())
	if err == nil || report.Verified || constructed != 0 || len(api.calls) != 0 {
		t.Fatalf("范围外域名不应构造客户端或调用云 API：report=%+v constructed=%d calls=%+v err=%v", report, constructed, api.calls, err)
	}
}

func TestAliyunAuthorizationVerifyFailurePathsAlwaysPreserveProofBoundary(t *testing.T) {
	tests := []struct {
		name          string
		configure     func(*authorizationRecordAPI)
		wantCalls     string
		wantWritable  bool
		wantReadable  bool
		wantCleanup   bool
		wantErrorPart string
	}{
		{
			name:          "创建失败",
			configure:     func(api *authorizationRecordAPI) { api.addErr = errors.New("Forbidden.RAM") },
			wantCalls:     "add",
			wantErrorPart: "Forbidden.RAM",
		},
		{
			name:          "查询失败仍清理",
			configure:     func(api *authorizationRecordAPI) { api.describeErr = errors.New("Forbidden.RAM") },
			wantCalls:     "add,describe,delete",
			wantWritable:  true,
			wantCleanup:   true,
			wantErrorPart: "查询授权探测 TXT 记录失败",
		},
		{
			name: "查询内容不一致仍清理",
			configure: func(api *authorizationRecordAPI) {
				api.describeInfo = aliyunRecordInfo{RecordID: "other-record-id", DomainName: "sub.example.com", RR: "wrong", Type: "TXT", Value: "wrong"}
			},
			wantCalls:     "add,describe,delete",
			wantWritable:  true,
			wantCleanup:   true,
			wantErrorPart: "不一致",
		},
		{
			name:          "清理失败不能通过",
			configure:     func(api *authorizationRecordAPI) { api.deleteErr = errors.New("Forbidden.RAM") },
			wantCalls:     "add,describe,delete",
			wantWritable:  true,
			wantReadable:  true,
			wantErrorPart: "清理授权探测 TXT 记录失败",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &authorizationRecordAPI{}
			tc.configure(api)
			report, err := newAuthorizationCheckerForTest(api).Verify(t.Context(), "www.sub.example.com", scopeCredentials())
			if err == nil || !strings.Contains(err.Error(), tc.wantErrorPart) {
				t.Fatalf("错误结果不正确：err=%v", err)
			}
			if report.Verified || len(report.Domains) != 1 {
				t.Fatalf("失败路径不能报告已验证：%+v", report)
			}
			domain := report.Domains[0]
			if domain.Writable != tc.wantWritable || domain.Readable != tc.wantReadable || domain.Cleanup != tc.wantCleanup {
				t.Fatalf("失败路径状态错误：%+v", domain)
			}
			if got := callNames(api.calls); got != tc.wantCalls {
				t.Fatalf("失败路径 API 调用顺序错误：got=%s want=%s", got, tc.wantCalls)
			}
		})
	}
}

func TestAliyunAuthorizationVerifyCleansUpAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	api := &authorizationRecordAPI{cancelAfterAdd: cancel}
	report, err := newAuthorizationCheckerForTest(api).Verify(ctx, "www.sub.example.com", scopeCredentials())
	if !errors.Is(err, context.Canceled) || report.Verified {
		t.Fatalf("请求取消必须失败且不能报告已验证：report=%+v err=%v", report, err)
	}
	if got := callNames(api.calls); got != "add,describe,delete" {
		t.Fatalf("请求取消后必须完成受控清理：%s", got)
	}
	if api.cleanupWasCanceled {
		t.Fatal("清理请求不应继承已取消的上下文")
	}
}

func TestPermissionVerificationTimeoutForAliyunIsIndependentFromPropagation(t *testing.T) {
	if got := PermissionVerificationTimeoutFor(Aliyun); got != 10*time.Second {
		t.Fatalf("阿里云权限验证时限错误：%s", got)
	}
	if got := PermissionVerificationTimeoutFor(Cloudflare); got != 60*time.Second {
		t.Fatalf("其他 DNS 服务商权限验证时限错误：%s", got)
	}
}

func callNames(calls []authorizationCall) string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.name)
	}
	return strings.Join(names, ",")
}
