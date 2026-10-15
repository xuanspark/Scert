package domain

import "testing"

func TestNewDomainTypes(t *testing.T) {
	tests := []struct {
		name            string
		domainName      string
		certificateType CertificateType
		wantError       bool
	}{
		{name: "单域名", domainName: "Example.COM", certificateType: CertificateSingle},
		{name: "泛域名", domainName: "*.example.com", certificateType: CertificateWildcard},
		{name: "泛域名缺少前缀", domainName: "example.com", certificateType: CertificateWildcard, wantError: true},
		{name: "单域名使用泛域名", domainName: "*.example.com", certificateType: CertificateSingle, wantError: true},
		{name: "非法证书类型", domainName: "example.com", certificateType: CertificateType("invalid"), wantError: true},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			result, err := NewDomain(item.domainName, item.certificateType, nil)
			if item.wantError {
				if err == nil {
					t.Fatal("预期返回错误，实际未返回")
				}
				return
			}
			if err != nil {
				t.Fatalf("创建域名失败：%v", err)
			}
			if result.Name == "" || len(result.SANs) != 1 || result.SANs[0] != result.Name {
				t.Fatalf("域名规范化结果不正确：%+v", result)
			}
			if !result.AutoRenew || result.ACMEEnvironment != ACMEEnvironmentStaging {
				t.Fatalf("默认域名选项不正确：%+v", result)
			}
		})
	}
}

func TestNewDomainSANIncludesPrimaryAndDeduplicates(t *testing.T) {
	result, err := NewDomain("Example.com", CertificateSAN, []string{"www.example.com", "EXAMPLE.COM", "www.example.com"})
	if err != nil {
		t.Fatalf("创建 SAN 域名失败：%v", err)
	}
	if len(result.SANs) != 2 {
		t.Fatalf("SAN 去重结果不正确：%v", result.SANs)
	}
	if result.SANs[0] != "example.com" || result.SANs[1] != "www.example.com" {
		t.Fatalf("SAN 未自动包含主域名或顺序异常：%v", result.SANs)
	}
}

func TestNewDomainSANValidation(t *testing.T) {
	if _, err := NewDomain("example.com", CertificateSAN, []string{"example.com"}); err == nil {
		t.Fatal("仅包含主域名的 SAN 配置未被拒绝")
	}
	if _, err := NewDomain("example.com", CertificateSAN, []string{"bad_domain"}); err == nil {
		t.Fatal("非法 SAN 域名未被拒绝")
	}
}

func TestNewDomainSANAllowsWildcardIdentifier(t *testing.T) {
	result, err := NewDomain("x.x.x.com", CertificateSAN, []string{"*.x.x.x.com"})
	if err != nil {
		t.Fatalf("多级域名与对应泛域名的 SAN 配置不应失败：%v", err)
	}
	if len(result.SANs) != 2 || result.SANs[0] != "x.x.x.com" || result.SANs[1] != "*.x.x.x.com" {
		t.Fatalf("SAN 域名顺序或内容不正确：%v", result.SANs)
	}
}

func TestNewDomainOptionsValidation(t *testing.T) {
	item, err := NewDomainWithOptions("example.com", CertificateSingle, nil, false, ACMEEnvironmentProduction, "cloudflare")
	if err != nil {
		t.Fatalf("带选项创建域名失败：%v", err)
	}
	if item.AutoRenew || item.ACMEEnvironment != ACMEEnvironmentProduction || item.DNSProvider != "cloudflare" {
		t.Fatalf("域名选项保存不正确：%+v", item)
	}
	if _, err := NewDomainWithOptions("example.com", CertificateSingle, nil, true, "invalid", ""); err == nil {
		t.Fatal("非法 ACME 环境未被拒绝")
	}
}

func TestWildcardNamePreservesMultiLevelDomain(t *testing.T) {
	for _, item := range []struct {
		input string
		want  string
	}{
		{input: "x.x.com", want: "*.x.x.com"},
		{input: " X.X.X.COM ", want: "*.x.x.x.com"},
	} {
		got, err := WildcardName(item.input)
		if err != nil {
			t.Fatalf("WildcardName(%q) failed: %v", item.input, err)
		}
		if got != item.want {
			t.Fatalf("WildcardName(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, input := range []string{"*.x.x.com", "bad_domain"} {
		if _, err := WildcardName(input); err == nil {
			t.Fatalf("WildcardName(%q) should reject invalid base domain", input)
		}
	}
}
