package certificate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type DomainLister interface {
	List(ctx context.Context) ([]domain.Domain, error)
}

type CertificateIssuer interface {
	Issue(ctx context.Context, domainID string) (IssueResult, error)
}

type RenewalPolicy struct {
	CheckInterval       time.Duration
	NearExpiryThreshold time.Duration
	RenewalThreshold    time.Duration
	UrgentThreshold     time.Duration
	MaxRetries          int
	RetryInterval       time.Duration
}

type RenewalScheduler struct {
	lister  DomainLister
	issuer  CertificateIssuer
	policy  RenewalPolicy
	now     func() time.Time
	mu      sync.Mutex
	runMu   sync.Mutex
	running bool
}

type RenewalReport struct {
	Checked    int      `json:"checked"`
	NearExpiry int      `json:"near_expiry"`
	Selected   int      `json:"selected"`
	Urgent     int      `json:"urgent"`
	Succeeded  int      `json:"succeeded"`
	Failed     int      `json:"failed"`
	Errors     []string `json:"errors,omitempty"`
}

func NewRenewalScheduler(lister DomainLister, issuer CertificateIssuer, thresholdDays int) *RenewalScheduler {
	if thresholdDays < 1 {
		thresholdDays = 6
	}
	return NewRenewalSchedulerWithPolicy(lister, issuer, RenewalPolicy{
		CheckInterval:       3 * 24 * time.Hour,
		NearExpiryThreshold: 10 * 24 * time.Hour,
		RenewalThreshold:    time.Duration(thresholdDays) * 24 * time.Hour,
		UrgentThreshold:     3 * 24 * time.Hour,
		MaxRetries:          3,
		RetryInterval:       30 * time.Minute,
	})
}

func NewRenewalSchedulerWithPolicy(lister DomainLister, issuer CertificateIssuer, policy RenewalPolicy) *RenewalScheduler {
	policy = normalizeRenewalPolicy(policy)
	return &RenewalScheduler{lister: lister, issuer: issuer, policy: policy, now: time.Now}
}

func (s *RenewalScheduler) RunOnce(ctx context.Context) (RenewalReport, error) {
	if s == nil || s.lister == nil || s.issuer == nil {
		return RenewalReport{}, errors.New("自动续期服务未初始化")
	}
	if !s.runMu.TryLock() {
		return RenewalReport{}, errors.New("自动续期任务正在执行")
	}
	defer s.runMu.Unlock()
	items, err := s.lister.List(ctx)
	if err != nil {
		return RenewalReport{}, fmt.Errorf("查询续期域名失败：%w", err)
	}
	report := RenewalReport{Checked: len(items)}
	now := s.now().UTC()
	for _, item := range items {
		if !item.AutoRenew || item.Status == "issuing" {
			continue
		}
		remaining := renewalRemaining(item.ExpiresAt, now)
		if item.ExpiresAt != nil && remaining <= s.policy.NearExpiryThreshold {
			report.NearExpiry++
		}
		if item.ExpiresAt != nil && remaining > s.policy.RenewalThreshold {
			continue
		}
		report.Selected++
		urgent := item.ExpiresAt != nil && remaining <= s.policy.UrgentThreshold
		if urgent {
			report.Urgent++
		}
		if err := s.issueWithRetry(ctx, item.ID, urgent); err != nil {
			report.Failed++
			report.Errors = append(report.Errors, fmt.Sprintf("%s：%v", item.Name, err))
			continue
		}
		report.Succeeded++
	}
	return report, nil
}

func (s *RenewalScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		report, err := s.RunOnce(ctx)
		for {
			interval := s.policy.CheckInterval
			if err == nil && report.NearExpiry > 0 {
				interval = 24 * time.Hour
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
			report, err = s.RunOnce(ctx)
		}
	}()
}

func (s *RenewalScheduler) issueWithRetry(ctx context.Context, domainID string, urgent bool) error {
	attempts := 1
	if urgent {
		attempts = s.policy.MaxRetries
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if _, err := s.issuer.Issue(ctx, domainID); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == attempts {
			break
		}
		if err := waitForRenewalRetry(ctx, s.policy.RetryInterval); err != nil {
			return fmt.Errorf("第 %d 次续期失败，等待重试被取消：%w", attempt, err)
		}
	}
	return fmt.Errorf("续期失败（已尝试 %d 次）：%w", attempts, lastErr)
}

func waitForRenewalRetry(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func renewalRemaining(expiresAt *time.Time, now time.Time) time.Duration {
	if expiresAt == nil {
		return 0
	}
	return expiresAt.Sub(now)
}

func normalizeRenewalPolicy(policy RenewalPolicy) RenewalPolicy {
	if policy.CheckInterval <= 0 {
		policy.CheckInterval = 3 * 24 * time.Hour
	}
	if policy.NearExpiryThreshold <= 0 {
		policy.NearExpiryThreshold = 10 * 24 * time.Hour
	}
	if policy.RenewalThreshold <= 0 {
		policy.RenewalThreshold = 6 * 24 * time.Hour
	}
	if policy.UrgentThreshold <= 0 {
		policy.UrgentThreshold = 3 * 24 * time.Hour
	}
	if policy.MaxRetries <= 0 {
		policy.MaxRetries = 3
	}
	if policy.RetryInterval <= 0 {
		policy.RetryInterval = 30 * time.Minute
	}
	return policy
}
