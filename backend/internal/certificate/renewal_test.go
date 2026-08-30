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
	expires := now.Add(4 * 24 * time.Hour)
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

func TestRenewalSchedulerRetriesUrgentRenewal(t *testing.T) {
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	expires := now.Add(2 * 24 * time.Hour)
	service := &renewalService{err: errors.New("模拟紧急续期失败")}
	scheduler := NewRenewalSchedulerWithPolicy(renewalLister{items: []domain.Domain{{
		ID: "urgent", Name: "urgent.example.com", AutoRenew: true, ExpiresAt: &expires,
	}}}, service, RenewalPolicy{
		CheckInterval:       time.Hour,
		NearExpiryThreshold: 10 * 24 * time.Hour,
		RenewalThreshold:    6 * 24 * time.Hour,
		UrgentThreshold:     3 * 24 * time.Hour,
		MaxRetries:          3,
		RetryInterval:       time.Nanosecond,
	})
	scheduler.now = func() time.Time { return now }

	report, err := scheduler.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Selected != 1 || report.Urgent != 1 || report.Failed != 1 || report.Succeeded != 0 {
		t.Fatalf("紧急续期报告错误：%+v", report)
	}
	if len(service.ids) != 3 {
		t.Fatalf("紧急续期应按最大重试次数执行：%v", service.ids)
	}
}

func TestRenewalSchedulerStopsRetryWhenContextCanceled(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(2 * 24 * time.Hour)
	service := &renewalService{err: errors.New("模拟紧急续期失败")}
	scheduler := NewRenewalSchedulerWithPolicy(renewalLister{items: []domain.Domain{{
		ID: "urgent", Name: "urgent.example.com", AutoRenew: true, ExpiresAt: &expires,
	}}}, service, RenewalPolicy{MaxRetries: 3, RetryInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	report, err := scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || len(service.ids) != 1 {
		t.Fatalf("取消后的紧急续期重试报告错误：%+v calls=%d", report, len(service.ids))
	}
}

type wakeRenewalLister struct {
	calls chan struct{}
}

func (l *wakeRenewalLister) List(context.Context) ([]domain.Domain, error) {
	select {
	case l.calls <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestRenewalSchedulerUpdatePolicyWakesRunningScheduler(t *testing.T) {
	lister := &wakeRenewalLister{calls: make(chan struct{}, 4)}
	scheduler := NewRenewalSchedulerWithPolicy(lister, &renewalService{}, RenewalPolicy{
		CheckInterval:       time.Hour,
		NearExpiryThreshold: 10 * 24 * time.Hour,
		RenewalThreshold:    6 * 24 * time.Hour,
		UrgentThreshold:     3 * 24 * time.Hour,
		MaxRetries:          3,
		RetryInterval:       time.Minute,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx)
	select {
	case <-lister.calls:
	case <-time.After(time.Second):
		t.Fatal("\u7eed\u671f\u8c03\u5ea6\u5668\u542f\u52a8\u540e\u672a\u6267\u884c\u9996\u6b21\u68c0\u67e5")
	}

	scheduler.UpdatePolicy(RenewalPolicy{
		CheckInterval:       2 * time.Hour,
		NearExpiryThreshold: 12 * 24 * time.Hour,
		RenewalThreshold:    8 * 24 * time.Hour,
		UrgentThreshold:     4 * 24 * time.Hour,
		MaxRetries:          5,
		RetryInterval:       2 * time.Minute,
	})
	if got := scheduler.Policy(); got.CheckInterval != 2*time.Hour || got.NearExpiryThreshold != 12*24*time.Hour || got.RenewalThreshold != 8*24*time.Hour || got.UrgentThreshold != 4*24*time.Hour || got.MaxRetries != 5 || got.RetryInterval != 2*time.Minute {
		t.Fatalf("\u8fd0\u884c\u65f6\u7eed\u671f\u7b56\u7565\u672a\u66f4\u65b0\uff1a%+v", got)
	}
	select {
	case <-lister.calls:
	case <-time.After(time.Second):
		t.Fatal("\u66f4\u65b0\u7eed\u671f\u7b56\u7565\u540e\u8c03\u5ea6\u5668\u672a\u88ab\u7acb\u5373\u5524\u9192")
	}
}
