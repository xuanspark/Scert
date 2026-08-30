package dnsprovider

import "errors"

var (
	ErrInvalidDelegatedDomain       = errors.New("CNAME 委派域名无效")
	ErrInvalidDelegatedCredentials  = errors.New("CNAME 委派凭据无效")
	ErrDelegatedProviderUnavailable = errors.New("CNAME 委派目标 Provider 不可用")
)
