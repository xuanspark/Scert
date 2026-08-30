package dnsprovider

import (
	"context"
	"strings"

	"github.com/go-acme/lego/v5/challenge"
)

// cnameDelegationProvider 将原始域名的 DNS-01 挑战转发到专用验证域。
// 委派域上的 TXT 记录由目标 Provider 管理，原始域名上的 CNAME 记录由用户提前配置。
type cnameDelegationProvider struct {
	delegatedDomain string
	target          challenge.Provider
}

func newCNAMEDelegationProvider(delegatedDomain string, target challenge.Provider) (challenge.Provider, error) {
	delegatedDomain = normalizeProviderDomain(delegatedDomain)
	if delegatedDomain == "" {
		return nil, ErrInvalidDelegatedDomain
	}
	if target == nil {
		return nil, ErrDelegatedProviderUnavailable
	}
	return &cnameDelegationProvider{delegatedDomain: delegatedDomain, target: target}, nil
}

func (p *cnameDelegationProvider) Present(ctx context.Context, _ string, token, keyAuth string) error {
	return p.target.Present(ctx, p.delegatedDomain, token, keyAuth)
}

func (p *cnameDelegationProvider) CleanUp(ctx context.Context, _ string, token, keyAuth string) error {
	return p.target.CleanUp(ctx, p.delegatedDomain, token, keyAuth)
}

func normalizeProviderDomain(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}
