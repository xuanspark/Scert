package certificate

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
)

type httpTestIssuer func(context.Context, ObtainRequest) (Resource, error)

func (f httpTestIssuer) Obtain(ctx context.Context, request ObtainRequest) (Resource, error) {
	return f(ctx, request)
}

func TestServiceHTTPChallengeDoesNotNeedDNSAndCleansAfterIssue(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			cfg := certificateTestConfig(t.TempDir())
			cfg.DNS = config.DNSConfig{}
			item, err := domain.NewDomain("example.com", domain.CertificateSingle, nil)
			if err != nil {
				t.Fatal(err)
			}
			item.ChallengeType = domain.ChallengeHTTP01
			repo := &serviceRepository{item: item}
			service := NewService(cfg, repo, nil, nil, serviceAccounts{})
			store := httpchallenge.NewStore()
			service.SetHTTPChallenges(store)
			resource := testResource(t, "example.com")
			service.acmeRoots = testResourceRoots(t, resource)
			const token = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
			const auth = token + "." + token
			checker := &serviceAuthorizationChecker{}
			service.SetDNSAuthorizationChecker(checker)
			service.newIssuer = func(_ *AccountUser, provider challenge.Provider, _ string) Issuer {
				return httpTestIssuer(func(ctx context.Context, request ObtainRequest) (Resource, error) {
					if request.ChallengeType != domain.ChallengeHTTP01 {
						t.Fatal("未传递 HTTP 验证方式")
					}
					if err := provider.Present(ctx, item.Name, token, auth); err != nil {
						t.Fatal(err)
					}
					w := httptest.NewRecorder()
					store.ServeHTTP(w, httptest.NewRequest("GET", "http://example.com"+httpchallenge.PathPrefix+token, nil))
					if w.Code != 200 || w.Body.String() != auth {
						t.Fatal("签发期间挑战不可读")
					}
					// 模拟签发器遗漏清理，服务自身仍须在任意返回路径回收订单。
					if failure {
						return Resource{}, errors.New("模拟签发失败")
					}
					return resource, nil
				})
			}
			_, err = service.Issue(context.Background(), item.ID)
			if (err != nil) != failure {
				t.Fatal("签发结果不符合预期：", err)
			}
			if checker.verifyCalls != 0 || checker.invalidateCalls != 0 {
				t.Fatal("HTTP 验证触碰 DNS 授权")
			}
			w := httptest.NewRecorder()
			store.ServeHTTP(w, httptest.NewRequest("GET", "http://example.com"+httpchallenge.PathPrefix+token, nil))
			if w.Code != 404 {
				t.Fatal("签发结束后仍残留挑战")
			}
		})
	}
}

func TestServiceHTTPRejectsWildcardSANBeforeDependencies(t *testing.T) {
	item := domain.Domain{ID: "wild", Name: "example.com", CertificateType: domain.CertificateSAN, SANs: []string{"example.com", "*.example.com"}, ChallengeType: domain.ChallengeHTTP01}
	repo := &serviceRepository{item: item}
	service := NewService(certificateTestConfig(t.TempDir()), repo, nil, nil, serviceAccounts{})
	service.SetHTTPChallenges(httpchallenge.NewStore())
	called := false
	service.newIssuer = func(*AccountUser, challenge.Provider, string) Issuer { called = true; return nil }
	if _, err := service.Issue(context.Background(), item.ID); err == nil {
		t.Fatal("未拒绝泛域名 SAN")
	}
	if called || len(repo.states) != 0 {
		t.Fatal("拒绝前产生签发副作用")
	}
}
