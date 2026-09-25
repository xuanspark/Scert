package certificate

import (
	"context"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type recordedRenewalExecutor struct {
	calls  int
	urgent bool
	policy RenewalPolicy
}

func (r *recordedRenewalExecutor) Issue(context.Context, string) (IssueResult, error) {
	panic("unexpected fallback")
}

func (r *recordedRenewalExecutor) Renew(_ context.Context, _ string, urgent bool, policy RenewalPolicy) error {
	r.calls++
	r.urgent, r.policy = urgent, policy
	return nil
}

func TestRenewalSchedulerUsesRecordedExecutor(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	executor := &recordedRenewalExecutor{}
	scheduler := NewRenewalScheduler(renewalLister{items: []domain.Domain{{ID: "domain", AutoRenew: true, ExpiresAt: &expires}}}, executor, 6)
	report, err := scheduler.RunOnce(t.Context())
	if err != nil || report.Succeeded != 1 || executor.calls != 1 || !executor.urgent || executor.policy.MaxRetries != 3 {
		t.Fatalf("调度器未使用带记录的续期入口：%+v %+v %v", report, executor, err)
	}
}
