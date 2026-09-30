package dnsprovider

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestAliyunAuthorizationRecordLabelUsesSafeRandomSuffix(t *testing.T) {
	for _, name := range []string{"example.com", "web.example.com", "*.web.example.com", "www.sub.example.com"} {
		t.Run(name, func(t *testing.T) {
			seen := make(map[string]bool)
			for attempt := 0; attempt < 32; attempt++ {
				api := &authorizationRecordAPI{}
				report, err := newAuthorizationCheckerForTest(api).Verify(t.Context(), name, scopeCredentials())
				if err != nil || !report.Verified {
					t.Fatalf("授权闭环验证失败：report=%+v err=%v", report, err)
				}
				label := strings.SplitN(api.rr, ".", 2)[0]
				const prefix = "_scert-authorization-"
				suffix := strings.TrimPrefix(label, prefix)
				if !strings.HasPrefix(label, prefix) || len(label) > 63 || len(suffix) != 36 {
					t.Fatalf("随机主机记录标签必须使用固定长度安全后缀：%q", label)
				}
				decoded, err := hex.DecodeString(suffix)
				if err != nil || len(decoded) != 18 || suffix != strings.ToLower(suffix) {
					t.Fatalf("随机后缀必须完整编码十八字节随机数且只使用小写十六进制：%q", suffix)
				}
				if seen[suffix] {
					t.Fatalf("不同请求复用了随机探测后缀：%q", suffix)
				}
				seen[suffix] = true
				targetSuffix := strings.TrimSuffix(strings.TrimPrefix(name, "*."), ".example.com")
				if name == "example.com" {
					targetSuffix = ""
				} else {
					targetSuffix = "." + targetSuffix
				}
				if api.rr != label+targetSuffix || report.RecordName != api.rr+".example.com." {
					t.Fatalf("探测记录越出指定域名范围：rr=%q record=%q", api.rr, report.RecordName)
				}
				value, err := base64.RawURLEncoding.DecodeString(api.value)
				if err != nil || len(value) != 32 || callNames(api.calls) != "add,describe,delete" {
					t.Fatalf("记录名修复不应改变 TXT 随机强度或写读清理闭环：value=%q calls=%v", api.value, api.calls)
				}
			}
		})
	}
}

func TestAliyunInvalidRecordFormatDiagnosisDoesNotSuggestPermissionChange(t *testing.T) {
	message := DiagnoseAliyunError(errors.New("SDKError: Code: InvalidRR.Format secret-provider-detail"), "web.example.com")
	if !strings.Contains(message, "记录名格式") || !strings.Contains(message, "权限不足") || strings.Contains(message, "secret-provider-detail") {
		t.Fatalf("随机记录名格式错误应提供脱敏且不误导扩权的说明：%s", message)
	}
}
