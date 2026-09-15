package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
)

func TestRenewalReportHidesNestedWorkflowErrors(t *testing.T) {
	server := New(Dependencies{
		Config: readyTestConfig(t.TempDir()),
		RunRenewal: func(context.Context) (certificate.RenewalReport, error) {
			return certificate.RenewalReport{Failed: 1, Errors: []string{"internal-secret-sentinel"}}, nil
		},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/renewal/run", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "internal-secret-sentinel") {
		t.Fatalf("续期报告不得返回底层敏感错误：%d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"failed":1`) {
		t.Fatal("脱敏不能丢失续期失败计数")
	}
}

type unsafeReportChecker struct {
	fakeAliyunScopeChecker
}

func (f *unsafeReportChecker) Verify(context.Context, string, map[string]string) (dnsprovider.AuthorizationReport, error) {
	err := errors.New("internal-secret-sentinel")
	return dnsprovider.AuthorizationReport{
		Diagnosis: err.Error(),
		Domains:   []dnsprovider.AuthorizationDomain{{Domain: "example.test", Writable: true, Cleanup: false, Error: err.Error()}},
	}, err
}

func TestAliyunReportHidesSDKErrorAndPreservesCleanupFailure(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.DNS.Provider = dnsprovider.Aliyun
	cfg.DNS.CredentialRef = "secret://test/only"
	server := New(Dependencies{
		Config: cfg, AliyunChecker: &unsafeReportChecker{},
		Secrets: &fakeSecretReaderStore{data: map[string]map[string]string{cfg.DNS.CredentialRef: {"access_key_id": "test-id", "access_key_secret": "test-secret"}}},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/dns/aliyun/check", strings.NewReader(`{"domain":"example.test"}`)))
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Contains(body, "internal-secret-sentinel") {
		t.Fatalf("阿里云验证报告不得返回 SDK 敏感错误：%d", response.Code)
	}
	if !strings.Contains(body, `"writable":true`) || !strings.Contains(body, `"cleanup":false`) || !strings.Contains(body, `"verified":false`) {
		t.Fatal("脱敏必须保留写入成功但清理失败的验收事实")
	}
}
