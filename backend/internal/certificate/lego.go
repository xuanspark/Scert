package certificate

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	User        *AccountUser
	Provider    challenge.Provider
	Environment string
	HTTPClient  *http.Client
	Now         func() time.Time
}

func (i *LegoIssuer) Obtain(ctx context.Context, request ObtainRequest) (Resource, error) {
	if i == nil || i.User == nil || i.User.PrivateKey == nil {
		return Resource{}, errors.New("ACME 账户未配置")
	}
	if i.Provider == nil {
		return Resource{}, errors.New("DNS 验证服务商未配置")
	}
	domains, err := NormalizeDomains(request.Domains)
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
	if err := client.Challenge.SetDNS01Provider(i.Provider); err != nil {
		return Resource{}, fmt.Errorf("配置 DNS-01 验证失败：%w", err)
	}
	if i.User.Registration == nil {
		account, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return Resource{}, fmt.Errorf("注册 ACME 账户失败：%w", err)
		}
		i.User.Registration = account
	}
	issued, err := client.Certificate.Obtain(ctx, legoCertificate.ObtainRequest{
		Domains: domains, EmailAddresses: nonEmptyEmail(request.AccountEmail, i.User.Email),
		KeyType: certcrypto.RSA2048, Bundle: true,
		PreferredChain: request.PreferredChain,
	})
	if err != nil {
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

func nonEmptyEmail(values ...string) []string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return []string{strings.TrimSpace(value)}
		}
	}
	return nil
}
