package certificate

import (
	"context"
	"crypto/x509"
	"embed"
	"fmt"
	"strings"
)

// 测试根仅供 ACME 测试签发结果校验，不加入系统、HTTP 客户端或正式环境信任库。
//
//go:embed trust/*.pem
var stagingTrust embed.FS

func acmeTrustRoots(environment string) (*x509.CertPool, error) {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "production":
		return nil, nil
	case "staging":
		roots := x509.NewCertPool()
		entries, err := stagingTrust.ReadDir("trust")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			data, err := stagingTrust.ReadFile("trust/" + entry.Name())
			if err != nil || !roots.AppendCertsFromPEM(data) {
				return nil, fmt.Errorf("内置 ACME 测试根证书无效：%s", entry.Name())
			}
		}
		return roots, nil
	default:
		return nil, fmt.Errorf("ACME 环境无效：%s", environment)
	}
}



// CombinedTrustRoots 返回包含系统根证书以及指定环境内置根证书（如 staging 测试根）的证书池。
// 用于验证公网生产证书与测试环境签发的证书。
func CombinedTrustRoots(environment string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	env := strings.ToLower(strings.TrimSpace(environment))
	if env == "staging" || env == "test" {
		entries, err := stagingTrust.ReadDir("trust")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			data, err := stagingTrust.ReadFile("trust/" + entry.Name())
			if err == nil {
				pool.AppendCertsFromPEM(data)
			}
		}
	}
	return pool, nil
}

func (s *Service) validateACMEResource(ctx context.Context, resource Resource, domains []string, environment string) error {
	roots, err := acmeTrustRoots(environment)
	if err != nil {
		return err
	}
	if s.acmeRoots != nil {
		roots = s.acmeRoots
	}
	chain := append([]byte(nil), resource.Certificate...)
	chain = append(chain, resource.IssuerCertificate...)
	// 即使签发器未返回单独的 issuer 字段，也必须验证完整信任链。
	_, err = (LocalValidator{Roots: roots, VerifyChain: true}).ValidatePEM(ctx, chain, domains)
	if err != nil {
		return fmt.Errorf("ACME %s 证书信任链校验失败：%w", environment, err)
	}
	return nil
}
