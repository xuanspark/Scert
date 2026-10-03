package certificate

import (
	"context"
	"crypto/x509"
	"net/http"
	"slices"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
)

// 使用真实 lego 客户端、签名请求、挑战服务和动态 CSR 证书，不替换签发器。
// 隔离 CA 只验证本产品用到的协议流程，不代表公共 CA 或公网签发验收。
func TestLegoHTTP01Protocol(t *testing.T) {
	for _, item := range []struct {
		name        string
		domains     []string
		environment string
		reject      bool
		cancel      bool
	}{
		{name: "single", domains: []string{"example.com"}, environment: "production"},
		{name: "san", domains: []string{"example.com", "www.example.com"}, environment: "staging"},
		{name: "rejected", domains: []string{"example.com", "www.example.com"}, reject: true},
		{name: "canceled", domains: []string{"example.com"}, cancel: true},
	} {
		t.Run(item.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store := httpchallenge.NewStore()
			lease, err := store.NewLease(ctx, item.domains)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			ca := newProtocolCA(t, store, item.domains)
			ca.reject = item.reject
			if item.cancel {
				ca.cancel = cancel
			}
			issuer := &LegoIssuer{User: ca.user, Provider: lease, HTTPClient: ca.client}
			resource, err := issuer.Obtain(ctx, ObtainRequest{
				Domains: item.domains, ChallengeType: domain.ChallengeHTTP01, Environment: item.environment,
			})
			if item.reject || item.cancel {
				if err == nil || len(resource.Certificate) != 0 || len(resource.PrivateKey) != 0 {
					t.Fatal("失败或取消的 ACME 订单返回了成功证书")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ValidateResourceForDomains(resource, item.domains); err != nil {
					t.Fatal("实际 CSR 证书或密钥不匹配：", err)
				}
				roots := x509.NewCertPool()
				roots.AddCert(ca.root)
				validator := LocalValidator{Roots: roots, VerifyChain: true}
				if _, err := validator.ValidatePEM(ctx, resource.Certificate, item.domains); err != nil {
					t.Fatal("隔离 CA 返回的证书链校验失败：", err)
				}
			}
			ca.mu.Lock()
			defer ca.mu.Unlock()
			if !ca.registered || ca.proofs == 0 || len(ca.failures) != 0 {
				t.Fatalf("协议没有按预期完成：注册=%t，挑战=%d，异常=%v", ca.registered, ca.proofs, ca.failures)
			}
			if item.reject || item.cancel {
				if ca.finalized || ca.downloads != 0 {
					t.Fatal("失败订单不应提交 CSR 或下载证书")
				}
			} else if !ca.finalized || ca.downloads != 1 || ca.proofs != len(item.domains) || !slices.Equal(ca.csrNames, item.domains) {
				t.Fatal("成功订单缺少逐域名授权、CSR 或证书下载")
			}
			// 在调用 Lease.Close 之前核对，避免测试清理掩盖 lego 未回收挑战。
			for i, name := range item.domains {
				code, _, err := ca.readProof(context.Background(), name, protocolToken(i))
				if err != nil || code != http.StatusNotFound {
					t.Fatalf("订单结束后挑战仍可读：域名=%s，状态=%d，错误=%v", name, code, err)
				}
			}
		})
	}
}
