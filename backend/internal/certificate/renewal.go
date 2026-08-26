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

type RenewalScheduler struct {
	lister    DomainLister
	issuer    CertificateIssuer
	threshold time.Duration
	now       func() time.Time
	interval  time.Duration
	mu        sync.Mutex
	runMu     sync.Mutex
	running   bool
}

type RenewalReport struct {
	Checked   int      `json:"checked"`
	Selected  int      `json:"selected"`
	Succeeded int      `json:"succeeded"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors,omitempty"`
}

func NewRenewalScheduler(lister DomainLister, issuer CertificateIssuer, thresholdDays int) *RenewalScheduler {
	if thresholdDays < 1 {
		thresholdDays = 6
	}
	return &RenewalScheduler{
		lister: lister, issuer: issuer, threshold: time.Duration(thresholdDays) * 24 * time.Hour,
		now: time.Now, interval: 24 * time.Hour,
	}
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
		if item.ExpiresAt != nil && item.ExpiresAt.Sub(now) > s.threshold {
			continue
		}
		report.Selected++
		if _, err := s.issuer.Issue(ctx, item.ID); err != nil {
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
		_, _ = s.RunOnce(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.RunOnce(ctx)
			}
		}
	}()
}
