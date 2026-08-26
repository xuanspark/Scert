package dnsprovider

import (
	"fmt"
	"strings"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
)

type LegoFactory struct{}

func NewLegoFactory() LegoFactory {
	return LegoFactory{}
}

func (LegoFactory) Create(providerID string, credentials map[string]string) (challenge.Provider, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	descriptor, err := GetDescriptor(providerID)
	if err != nil {
		return nil, err
	}
	if !descriptor.Available {
		return nil, fmt.Errorf("%w：%s", ErrProviderUnavailable, descriptor.Name)
	}
	switch providerID {
	case Cloudflare:
		cfg := cloudflare.NewDefaultConfig()
		cfg.AuthToken = credentials["api_token"]
		cfg.ZoneToken = credentials["zone_token"]
		if cfg.ZoneToken == "" {
			cfg.ZoneToken = cfg.AuthToken
		}
		provider, err := cloudflare.NewDNSProviderConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建 Cloudflare DNS Provider 失败：%w", err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("%w：%s", ErrProviderUnavailable, descriptor.Name)
	}
}
