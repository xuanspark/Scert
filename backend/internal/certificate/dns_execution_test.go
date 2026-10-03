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

type executionAuthorization func(context.Context, string, string, map[string]string) error

func (a executionAuthorization) VerifyDomain(ctx context.Context, provider, name string, credentials map[string]string) error {
	return a(ctx, provider, name, credentials)
}

func TestIndependentDNSExecutionLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "acquire-failed", "validate-failed", "credential-failed", "present-failed", "cleanup-failed", "missing-cleanup", "release-failed", "cancelled", "revoked-before-present", "revoked-before-cleanup", "authorization-failed", "revoked-before-authorization", "cancelled-before-authorization"} {
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
			authorizationCalls := 0
			service.authorization = executionAuthorization(func(ctx context.Context, id, name string, credentials map[string]string) error {
				authorizationCalls++
				if id != "cloudflare" || name != item.Name || credentials["api_token"] != "independent-test-only" {
					t.Fatal("授权探测未使用租约的固定账户")
				}
				if scenario == "authorization-failed" {
					return failure
				}
				return nil
			})
			ctx = WithProgressReporter(ctx, func(_ context.Context, _, message string) error {
				if strings.HasPrefix(message, "正在验证域名 ") {
					if scenario == "revoked-before-authorization" {
						store.validateErr = failure
					}
					if scenario == "cancelled-before-authorization" {
						cancel()
					}
				}
				return nil
			})
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
			retained := scenario == "present-failed" || scenario == "cleanup-failed" || scenario == "missing-cleanup" || scenario == "acquire-failed" || scenario == "authorization-failed"
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
			if scenario == "authorization-failed" && (authorizationCalls != 1 || !strings.Contains(err.Error(), "授权探测清理尚未确认")) {
				t.Fatal("探测失败后未保留不确定清理引用")
			}
			if (scenario == "revoked-before-authorization" || scenario == "cancelled-before-authorization") && authorizationCalls != 0 {
				t.Fatal("撤权或取消后仍发起远端授权探测")
			}
		})
	}
}

func TestIndependentDNSAuthorizationEachDomainRevalidates(t *testing.T) {
	store := &executionLeaseStub{}
	calls := 0
	checker := &leasedDNSAuthorization{store: store, checker: executionAuthorization(func(context.Context, string, string, map[string]string) error {
		calls++
		store.validateErr = errors.New("权限已撤回")
		return nil
	})}
	service := &Service{}
	err := service.verifyDNSAuthorizationUsing(t.Context(), "cloudflare", []string{"one.example.test", "two.example.test"}, nil, checker)
	if err == nil || calls != 1 || store.validated != 2 || !checker.clean() {
		t.Fatal("第二个域名前撤权应停止探测，已确认清理的引用可释放", err, calls, store.validated)
	}
}

func TestIndependentDNSAuthorizationFailureCannotBeClearedByLaterSuccess(t *testing.T) {
	store := &executionLeaseStub{}
	calls := 0
	checker := &leasedDNSAuthorization{store: store, checker: executionAuthorization(func(context.Context, string, string, map[string]string) error {
		calls++
		if calls == 1 {
			return errors.New("探测远端状态不确定")
		}
		return nil
	})}
	if err := checker.VerifyDomain(t.Context(), "cloudflare", "one.example.test", nil); err == nil {
		t.Fatal("首次探测应失败")
	}
	if err := checker.VerifyDomain(t.Context(), "cloudflare", "two.example.test", nil); err != nil {
		t.Fatal(err)
	}
	if checker.clean() {
		t.Fatal("后续域名成功不能清除先前未知外部状态")
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
