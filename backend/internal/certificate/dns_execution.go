package certificate

import (
	"context"
	"errors"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"github.com/go-acme/lego/v5/challenge"
)

type DNSExecutionLeaseStore interface {
	AcquireDNSExecutionLease(context.Context, domain.Domain, domain.DNSExecutionIdentity) (domain.DNSExecutionLease, error)
	ValidateDNSExecutionLease(context.Context, domain.DNSExecutionLease) error
	ReleaseDNSExecutionLease(context.Context, string) error
}

type dnsExecutionContextKey struct{}

func WithDNSExecutionIdentity(ctx context.Context, identity domain.DNSExecutionIdentity) context.Context {
	return context.WithValue(ctx, dnsExecutionContextKey{}, identity)
}

func (s *Service) SetDNSExecutionLeaseStore(store DNSExecutionLeaseStore) {
	if s != nil {
		s.dnsLeases = store
	}
}

// 每次写挑战前再次检查任务和权限；清理只能使用原 Provider 和固定凭据。
// 失败的 Present 也可能已写入远端，因此必须等待匹配的 CleanUp 成功才释放引用。
type leasedDNSProvider struct {
	challenge.Provider
	store   DNSExecutionLeaseStore
	lease   domain.DNSExecutionLease
	mu      sync.Mutex
	pending map[[3]string]bool
}

func (p *leasedDNSProvider) Present(ctx context.Context, name, token, keyAuth string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.store.ValidateDNSExecutionLease(ctx, p.lease); err != nil {
		return errors.New("DNS 执行授权已失效，未写入挑战记录")
	}
	key := [3]string{name, token, keyAuth}
	p.mu.Lock()
	p.pending[key] = true
	p.mu.Unlock()
	return p.Provider.Present(ctx, name, token, keyAuth)
}

func (p *leasedDNSProvider) CleanUp(ctx context.Context, name, token, keyAuth string) error {
	// 任务取消不取消必要的挑战清理；清理自身仍有明确的时间上限。
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if err := p.Provider.CleanUp(cleanupCtx, name, token, keyAuth); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.pending, [3]string{name, token, keyAuth})
	p.mu.Unlock()
	return nil
}

func (p *leasedDNSProvider) clean() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) == 0
}

// 不覆盖服务商自己的传播等待参数。
func (p *leasedDNSProvider) Timeout() (time.Duration, time.Duration) {
	if timed, ok := p.Provider.(interface {
		Timeout() (time.Duration, time.Duration)
	}); ok {
		return timed.Timeout()
	}
	return time.Minute, 2 * time.Second
}
