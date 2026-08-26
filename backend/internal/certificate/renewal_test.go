package certificate

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type renewalLister struct {
	items []domain.Domain
	err   error
}

func (r renewalLister) List(context.Context) ([]domain.Domain, error) { return r.items, r.err }

type renewalService struct {
	ids []string
	err error
}

func (s *renewalService) Issue(_ context.Context, id string) (IssueResult, error) {
	s.ids = append(s.ids, id)
	return IssueResult{}, s.err
}

func TestRenewalSchedulerSelectsOnlyExpiringAutoRenewDomains(t *testing.T) {
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	soon := now.Add(2 * 24 * time.Hour)
	later := now.Add(20 * 24 * time.Hour)
	items := []domain.Domain{
		{ID: "soon", Name: "soon.example.com", AutoRenew: true, ExpiresAt: &soon},
		{ID: "later", Name: "later.example.com", AutoRenew: true, ExpiresAt: &later},
		{ID: "disabled", Name: "disabled.example.com", AutoRenew: false, ExpiresAt: &soon},
		{ID: "missing", Name: "missing.example.com", AutoRenew: true},
	}
	service := &renewalService{}
	scheduler := NewRenewalScheduler(renewalLister{items: items}, service, 6)
	scheduler.now = func() time.Time { return now }
	report, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Checked != 4 || report.Selected != 2 || report.Succeeded != 2 || report.Failed != 0 {
		t.Fatalf("续期筛选结果错误：%+v", report)
	}
	if len(service.ids) != 2 || service.ids[0] != "soon" || service.ids[1] != "missing" {
		t.Fatalf("续期执行域名错误：%v", service.ids)
	}
}

func TestRenewalSchedulerReportsFailures(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	item := domain.Domain{ID: "one", Name: "one.example.com", AutoRenew: true, ExpiresAt: &expires}
	scheduler := NewRenewalScheduler(renewalLister{items: []domain.Domain{item}}, &renewalService{err: errors.New("模拟续期失败")}, 6)
	report, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || len(report.Errors) != 1 {
		t.Fatalf("续期失败结果错误：%+v", report)
	}
}
