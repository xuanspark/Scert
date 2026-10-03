package certificate

import (
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func TestIndependentDNSAccountNeverFallsBackToSystemCredentials(t *testing.T) {
	item, err := domain.NewDomain("bound.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.DNSAccountID, item.DNSBindingID = "independent-account", "binding"
	repo := &serviceRepository{item: item}
	service := NewService(certificateTestConfig(t.TempDir()), repo, nil, nil, serviceAccounts{})
	if _, err := service.Issue(t.Context(), item.ID); err == nil || !strings.Contains(err.Error(), "执行租约") {
		t.Fatal("未接入执行租约时不能回退系统凭据", err)
	}
	if repo.updateCalls != 0 {
		t.Fatal("拒绝执行后不应变更域名状态")
	}
}
