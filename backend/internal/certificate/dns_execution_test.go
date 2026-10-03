package certificate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"github.com/go-acme/lego/v5/challenge"
)

type executionLeaseStub struct {
	lease                               domain.DNSExecutionLease
	acquireErr, validateErr, releaseErr error
	acquired, validated, released       int
}

func (s *executionLeaseStub) AcquireDNSExecutionLease(_ context.Context, _ domain.Domain, identity domain.DNSExecutionIdentity) (domain.DNSExecutionLease, error) {
	s.acquired++
	s.lease.Identity = identity
	return s.lease, s.acquireErr
}
func (s *executionLeaseStub) ValidateDNSExecutionLease(_ context.Context, lease domain.DNSExecutionLease) error {
	s.validated++
	if lease.CredentialRef != s.lease.CredentialRef {
		return errors.New("秘密引用被替换")
	}
	return s.validateErr
}
func (s *executionLeaseStub) ReleaseDNSExecutionLease(ctx context.Context, _ string) error {
	s.released++
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.releaseErr
}

type executionCredentials struct {
	reference string
	err       error
}

func (r *executionCredentials) Get(_ context.Context, reference string) (map[string]string, error) {
	r.reference = reference
	return map[string]string{"api_token": "independent-test-only"}, r.err
}

type executionProvider struct {
	presented, cleaned   int
	presentErr, cleanErr error
}

func (p *executionProvider) Present(context.Context, string, string, string) error {
	p.presented++
	return p.presentErr
}
func (p *executionProvider) CleanUp(ctx context.Context, _, _, _ string) error {
	p.cleaned++
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.cleanErr
}
func (p *executionProvider) Timeout() (time.Duration, time.Duration) {
	return 3 * time.Minute, 4 * time.Second
}

type executionFactory struct {
	provider challenge.Provider
	id       string
}

func (f *executionFactory) Create(id string, _ map[string]string) (challenge.Provider, error) {
	f.id = id
	return f.provider, nil
}

type executionIssuer struct {
	run func(context.Context) (Resource, error)
}

func (i executionIssuer) Obtain(ctx context.Context, _ ObtainRequest) (Resource, error) {
	return i.run(ctx)
}

func TestIndependentDNSExecutionLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "acquire-failed", "validate-failed", "credential-failed", "present-failed", "cleanup-failed", "missing-cleanup", "release-failed", "cancelled", "revoked-before-present", "revoked-before-cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := certificateTestConfig(t.TempDir())
			cfg.DNS.Provider, cfg.DNS.CredentialRef, cfg.DNS.VerificationState = "aliyun", "secret://dns/system-must-not-read", "unverified"
			item, err := domain.NewDomain("lease.example.test", domain.CertificateSingle, nil)
			if err != nil {
				t.Fatal(err)
			}
			item.DNSAccountID, item.DNSBindingID, item.DNSProvider = "account", "binding", "cloudflare"
			repo := &serviceRepository{item: item}
			store := &executionLeaseStub{lease: domain.DNSExecutionLease{ID: "lease", DomainID: item.ID, AccountID: "account", BindingID: "binding", Provider: "cloudflare", CredentialRef: "secret://dns/independent", Revision: 2}}
			reader := &executionCredentials{}
			provider := &executionProvider{}
			factory := &executionFactory{provider: provider}
			service := NewService(cfg, repo, reader, factory, serviceAccounts{})
			service.SetDNSExecutionLeaseStore(store)
			resource := testResourceForDomains(t, []string{item.Name})
			service.acmeRoots = testResourceRoots(t, resource)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = WithDNSExecutionIdentity(ctx, domain.DNSExecutionIdentity{TaskID: "task", AttemptID: "attempt", AttemptNumber: 2})
			failure := errors.New("测试失败")
			switch scenario {
			case "acquire-failed":
				store.acquireErr = failure
			case "validate-failed":
				store.validateErr = failure
			case "credential-failed":
				reader.err = failure
			case "present-failed":
				provider.presentErr = failure
			case "cleanup-failed":
				provider.cleanErr = failure
			case "release-failed":
				store.releaseErr = failure
			}
			service.newIssuer = func(_ *AccountUser, challengeProvider challenge.Provider, _ string) Issuer {
				return executionIssuer{run: func(ctx context.Context) (Resource, error) {
					if scenario == "revoked-before-present" {
						store.validateErr = failure
					}
					if timeout, interval := challengeProvider.(challenge.ProviderTimeout).Timeout(); timeout != 3*time.Minute || interval != 4*time.Second {
						t.Fatal("服务商传播参数丢失")
					}
					if err := challengeProvider.Present(ctx, item.Name, "token", "auth"); err != nil {
						return Resource{}, err
					}
					if scenario == "missing-cleanup" {
						return resource, nil
					}
					if scenario == "revoked-before-cleanup" {
						store.validateErr = failure
					}
					if scenario == "cancelled" {
						cancel()
					}
					if err := challengeProvider.CleanUp(ctx, item.Name, "token", "auth"); err != nil {
						return Resource{}, err
					}
					if err := ctx.Err(); err != nil {
						return Resource{}, err
					}
					return resource, nil
				}}
			}
			_, err = service.Issue(ctx, item.ID)
			wantSuccess := scenario == "success" || scenario == "revoked-before-cleanup"
			if (err == nil) != wantSuccess {
				t.Fatalf("场景结果不符：%v", err)
			}
			if store.acquired != 1 {
				t.Fatal("未获取真实执行租约")
			}
			if reader.reference == cfg.DNS.CredentialRef || (factory.id != "" && factory.id != "cloudflare") {
				t.Fatal("独立账户回退到系统配置")
			}
			if scenario != "acquire-failed" && scenario != "validate-failed" && reader.reference != "secret://dns/independent" {
				t.Fatal("未读取固定凭据")
			}
			retained := scenario == "present-failed" || scenario == "cleanup-failed" || scenario == "missing-cleanup" || scenario == "acquire-failed"
			if (store.released == 0) != retained {
				t.Fatalf("不确定挑战清理时释放引用，或正常收尾未释放：%d", store.released)
			}
			if scenario == "revoked-before-present" && provider.presented != 0 {
				t.Fatal("撤权后仍调用 DNS 服务商")
			}
			if scenario == "cancelled" && provider.cleaned != 1 {
				t.Fatal("取消不应跳过清理")
			}
			if scenario == "release-failed" && (err == nil || !strings.Contains(err.Error(), "租约释放失败")) {
				t.Fatal("释放失败未上报")
			}
		})
	}
}

func TestIndependentDNSExecutionRequiresTaskContext(t *testing.T) {
	item, _ := domain.NewDomain("lease.example.test", domain.CertificateSingle, nil)
	item.DNSAccountID, item.DNSBindingID = "account", "binding"
	repo := &serviceRepository{item: item}
	service := NewService(certificateTestConfig(t.TempDir()), repo, serviceCredentials{}, serviceProviderFactory{}, serviceAccounts{})
	store := &executionLeaseStub{}
	service.SetDNSExecutionLeaseStore(store)
	if _, err := service.Issue(t.Context(), item.ID); err == nil {
		t.Fatal("不能在任务之外使用独立凭据")
	}
	if store.acquired != 0 || repo.updateCalls != 0 {
		t.Fatal("拒绝前不应产生副作用")
	}
}
