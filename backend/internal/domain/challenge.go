package domain

import (
	"fmt"
	"strings"
)

const (
	ChallengeDNS01  = "dns-01"
	ChallengeHTTP01 = "http-01"
)

// NormalizeChallengeType 保持历史空值使用 DNS 验证，拒绝未知方式及所有泛域名。
func NormalizeChallengeType(value string, names []string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = ChallengeDNS01
	}
	if value != ChallengeDNS01 && value != ChallengeHTTP01 {
		return "", fmt.Errorf("证书验证方式无效：%s", value)
	}
	if value == ChallengeHTTP01 {
		for _, name := range names {
			if strings.HasPrefix(strings.TrimSpace(name), "*.") {
				return "", fmt.Errorf("HTTP-01 不支持泛域名，请使用 DNS-01")
			}
		}
	}
	return value, nil
}
