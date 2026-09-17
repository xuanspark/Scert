package certificate

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type managementRenewalFunc func(context.Context, RenewalPolicy, time.Time) (RenewalReport, error)

func (f managementRenewalFunc) RunRenewal(ctx context.Context, policy RenewalPolicy, now time.Time) (RenewalReport, error) {
	return f(ctx, policy, now)
}

func TestRenewalSchedulerChecksManagementWithoutBusinessDomains(t *testing.T) {
	calls := 0
	management := managementRenewalFunc(func(_ context.Context, policy RenewalPolicy, now time.Time) (RenewalReport, error) {
		calls++
		if policy.MaxRetries != 3 || now.IsZero() {
			t.Fatal("管理续期未收到当前策略和时间")
		}
		return RenewalReport{Checked: 1, Selected: 1, Succeeded: 1}, nil
	})
	s := NewRenewalSchedulerWithManagement(renewalLister{}, &renewalService{}, RenewalPolicy{}, management)
	report, err := s.RunOnce(t.Context())
	if err != nil || calls != 1 || report.Checked != 1 || report.Succeeded != 1 {
		t.Fatalf("空业务列表漏掉管理证书：%+v %v", report, err)
	}
}

func TestRenewalSchedulerKeepsBusinessRenewalAfterManagementFailure(t *testing.T) {
	managementErr := errors.New("模拟管理证书读取失败")
	management := managementRenewalFunc(func(context.Context, RenewalPolicy, time.Time) (RenewalReport, error) {
		return RenewalReport{Checked: 1, Selected: 1, Failed: 1}, managementErr
	})
	issuer := &renewalService{}
	s := NewRenewalSchedulerWithManagement(renewalLister{items: []domain.Domain{{ID: "business", AutoRenew: true}}}, issuer, RenewalPolicy{}, management)
	report, err := s.RunOnce(t.Context())
	if !errors.Is(err, managementErr) || report.Checked != 2 || report.Succeeded != 1 || report.Failed != 1 || len(issuer.ids) != 1 {
		t.Fatalf("管理错误阻断业务续期：%+v %v", report, err)
	}
}

func TestRenewalSchedulerChecksManagementBeforeFailedBusinessQuery(t *testing.T) {
	management := managementRenewalFunc(func(context.Context, RenewalPolicy, time.Time) (RenewalReport, error) {
		return RenewalReport{Checked: 1, Selected: 1, Succeeded: 1}, nil
	})
	s := NewRenewalSchedulerWithManagement(renewalLister{err: errors.New("模拟业务查询失败")}, &renewalService{}, RenewalPolicy{}, management)
	report, err := s.RunOnce(t.Context())
	if err == nil || report.Succeeded != 1 {
		t.Fatalf("业务查询失败导致遗漏管理续期：%+v %v", report, err)
	}
}

func TestRenewalSchedulerStopsManagementBeforeClosing(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	management := managementRenewalFunc(func(ctx context.Context, _ RenewalPolicy, _ time.Time) (RenewalReport, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return RenewalReport{}, ctx.Err()
	})
	s := NewRenewalSchedulerWithManagement(renewalLister{}, &renewalService{}, RenewalPolicy{}, management)
	s.Start(t.Context())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("管理调度未启动")
	}
	s.Stop()
	select {
	case <-stopped:
	default:
		t.Fatal("停止调度未等待管理任务退出")
	}
}

func TestRenewalSchedulerDoesNotLengthenShortCheckInterval(t *testing.T) {
	calls := make(chan struct{}, 2)
	management := managementRenewalFunc(func(context.Context, RenewalPolicy, time.Time) (RenewalReport, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return RenewalReport{Checked: 1, NearExpiry: 1}, nil
	})
	s := NewRenewalSchedulerWithManagement(renewalLister{}, &renewalService{}, RenewalPolicy{CheckInterval: 10 * time.Millisecond}, management)
	s.Start(t.Context())
	t.Cleanup(s.Stop)
	for i := 0; i < 2; i++ {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("临期规则不应将较短检查间隔延长为一天")
		}
	}
}
