package certificate

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	acme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	legoCertificate "github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	lego "github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/registration"
)

type AccountUser struct {
	Email        string
	PrivateKey   crypto.Signer
	Registration *acme.ExtendedAccount
}

func (u *AccountUser) GetEmail() string                       { return u.Email }
func (u *AccountUser) GetRegistration() *acme.ExtendedAccount { return u.Registration }
func (u *AccountUser) GetPrivateKey() crypto.Signer           { return u.PrivateKey }

type LegoIssuer struct {
	User                *AccountUser
	Provider            challenge.Provider
	Environment         string
	PropagationTimeout  time.Duration
	PropagationInterval time.Duration
	HTTPClient          *http.Client
	Now                 func() time.Time
}

type propagationProvider struct {
	challenge.Provider
	timeout  time.Duration
	interval time.Duration
}

func (p propagationProvider) Timeout() (time.Duration, time.Duration) {
	return p.timeout, p.interval
}

func withPropagationSettings(provider challenge.Provider, timeout, interval time.Duration) challenge.Provider {
	if provider == nil || timeout <= 0 || interval <= 0 || interval > timeout {
		return provider
	}
	return propagationProvider{Provider: provider, timeout: timeout, interval: interval}
}

func (i *LegoIssuer) Obtain(ctx context.Context, request ObtainRequest) (Resource, error) {
	if i == nil || i.User == nil || i.User.PrivateKey == nil {
		return Resource{}, errors.New("ACME 账户未配置")
	}
	if i.Provider == nil {
		return Resource{}, errors.New("证书验证服务未配置")
	}
	domains, err := NormalizeDomains(request.Domains)
	if err != nil {
		return Resource{}, err
	}
	challengeType, err := domain.NormalizeChallengeType(request.ChallengeType, domains)
	if err != nil {
		return Resource{}, err
	}
	environment := strings.ToLower(strings.TrimSpace(request.Environment))
	if environment == "" {
		environment = "production"
	}
	caURL := lego.DirectoryURLLetsEncrypt
	if environment == "staging" {
		caURL = lego.DirectoryURLLetsEncryptStaging
	} else if environment != "production" {
		return Resource{}, fmt.Errorf("ACME 环境无效：%s", environment)
	}
	clientConfig := lego.NewConfig(i.User)
	clientConfig.CADirURL = caURL
	clientConfig.UserAgent = "Scert/0.1"
	if i.HTTPClient != nil {
		clientConfig.HTTPClient = i.HTTPClient
	}
	client, err := lego.NewClient(clientConfig)
	if err != nil {
		return Resource{}, fmt.Errorf("创建 ACME 客户端失败：%w", err)
	}
	if challengeType == domain.ChallengeHTTP01 {
		if err := client.Challenge.SetHTTP01Provider(i.Provider); err != nil {
			return Resource{}, fmt.Errorf("配置 HTTP-01 验证失败：%w", err)
		}
	} else {
		provider := withPropagationSettings(i.Provider, i.PropagationTimeout, i.PropagationInterval)
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return Resource{}, fmt.Errorf("配置 DNS-01 验证失败：%w", err)
		}
	}
	if i.User.Registration == nil {
		account, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return Resource{}, fmt.Errorf("注册 ACME 账户失败：%w", err)
		}
		i.User.Registration = account
	}
	issued, err := client.Certificate.Obtain(ctx, legoCertificate.ObtainRequest{
		Domains: domains,
		KeyType: certcrypto.RSA2048, Bundle: true,
		PreferredChain: request.PreferredChain,
	})
	if err != nil {
		if challengeType == domain.ChallengeDNS01 && i.PropagationTimeout > 0 && i.PropagationInterval > 0 && i.PropagationInterval <= i.PropagationTimeout {
			return Resource{}, fmt.Errorf("申请证书失败（DNS 传播等待时间 %s，检测间隔 %s）：%w", i.PropagationTimeout, i.PropagationInterval, err)
		}
		return Resource{}, fmt.Errorf("申请证书失败：%w", err)
	}
	resource := Resource{
		Domains: issued.Domains, Certificate: issued.Certificate,
		IssuerCertificate: issued.IssuerCertificate, PrivateKey: issued.PrivateKey,
	}
	if i.Now != nil {
		_ = i.Now()
	}
	return ValidateResource(resource)
}
