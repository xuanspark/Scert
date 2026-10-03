package certificate

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
)

type DomainStateRepository interface {
	GetByID(ctx context.Context, id string) (domain.Domain, error)
	UpdateCertificateState(ctx context.Context, id, status string, expiresAt *time.Time) error
}

type CredentialReader interface {
	Get(ctx context.Context, reference string) (map[string]string, error)
}

type ProviderFactory interface {
	Create(providerID string, credentials map[string]string) (challenge.Provider, error)
}

// DNSAuthorizationChecker 在证书申请前确认当前凭据仍能操作目标域名。
type DNSAuthorizationChecker interface {
	VerifyDomain(ctx context.Context, providerID, requestedDomain string, credentials map[string]string) error
}

type DNSAuthorizationCacheInvalidator interface {
	InvalidateDomain(providerID, requestedDomain string, credentials map[string]string)
}

// ProgressReporter 用于把长耗时证书流程的阶段同步到任务中心。
type ProgressReporter func(context.Context, string, string) error

type progressReporterContextKey struct{}

// WithProgressReporter 将任务进度回调绑定到证书工作流上下文。
func WithProgressReporter(ctx context.Context, reporter ProgressReporter) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, progressReporterContextKey{}, reporter)
}

// ReportProgress 报告证书工作流的当前阶段；未绑定任务时不执行任何操作。
func ReportProgress(ctx context.Context, stage, message string) error {
	reporter, _ := ctx.Value(progressReporterContextKey{}).(ProgressReporter)
	if reporter == nil {
		return nil
	}
	return reporter(ctx, strings.TrimSpace(stage), strings.TrimSpace(message))
}

type AccountManager interface {
	LoadOrCreate(ctx context.Context, environment, email string) (*AccountUser, error)
	Save(ctx context.Context, environment string, user *AccountUser) error
}

type IssuerFactory func(user *AccountUser, provider challenge.Provider, environment string) Issuer

type Service struct {
	config         config.Config
	repository     DomainStateRepository
	credentials    CredentialReader
	providers      ProviderFactory
	authorization  DNSAuthorizationChecker
	accounts       AccountManager
	store          FileStore
	newIssuer      IssuerFactory
	preferredChain string
	acmeRoots      *x509.CertPool
	httpChallenges *httpchallenge.Store
	dnsLeases      DNSExecutionLeaseStore
}

func (s *Service) SetHTTPChallenges(store *httpchallenge.Store) {
	if s != nil {
		s.httpChallenges = store
	}
}

func (s *Service) SetDNSAuthorizationChecker(checker DNSAuthorizationChecker) {
	if s != nil {
		s.authorization = checker
	}
}

type IssueResult struct {
	DomainID string        `json:"domain_id"`
	Domains  []string      `json:"domains"`
	Saved    SavedResource `json:"saved"`
}

type ImportRequest struct {
	DomainID     string
	Certificate  string
	PrivateKey   string
	IssuerBundle string
}

type SetupCertificateRequest struct {
	Domains         []string
	CertificateType string
	ProviderID      string
	CredentialRef   string
	AccountEmail    string
	Environment     string
	CertificateRoot string
	BackupRoot      string
}

func NewService(cfg config.Config, repository DomainStateRepository, credentials CredentialReader, providers ProviderFactory, accounts AccountManager) *Service {
	return &Service{
		config: cfg, repository: repository, credentials: credentials, providers: providers, accounts: accounts,
		store: FileStore{
			Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
			ProtectedPaths: []string{cfg.Server.TLSCert, cfg.Server.TLSKey},
		},
		newIssuer: func(user *AccountUser, provider challenge.Provider, environment string) Issuer {
			return &LegoIssuer{
				User:                user,
				Provider:            provider,
				Environment:         environment,
				PropagationTimeout:  time.Duration(cfg.ACME.PropagationTimeoutSecs) * time.Second,
				PropagationInterval: time.Duration(cfg.ACME.PropagationPollingIntervalSecs) * time.Second,
			}
		},
	}
}

func (s *Service) Issue(ctx context.Context, domainID string) (result IssueResult, returnErr error) {
	if s == nil || s.repository == nil || s.accounts == nil || s.newIssuer == nil {
		return IssueResult{}, errors.New("证书申请服务未初始化")
	}
	domainID = strings.TrimSpace(domainID)
	if domainID == "" {
		return IssueResult{}, errors.New("域名 ID 不能为空")
	}
	item, err := s.repository.GetByID(ctx, domainID)
	if err != nil {
		return IssueResult{}, fmt.Errorf("读取域名配置失败：%w", err)
	}
	independent := item.DNSAccountID != "" || item.DNSBindingID != ""
	if independent && s.dnsLeases == nil {
		return IssueResult{}, errors.New("独立 DNS 账户执行租约尚未就绪，拒绝使用系统凭据替代")
	}
	domains, err := requestedDomains(item)
	if err != nil {
		return IssueResult{}, err
	}
	challengeType, err := domain.NormalizeChallengeType(item.ChallengeType, domains)
	if err != nil {
		return IssueResult{}, err
	}
	if err := authorizeIssueSnapshot(ctx, item); err != nil {
		return IssueResult{}, fmt.Errorf("签发配置权限校验失败：%w", err)
	}
	providerID := ""
	credentialRef := s.config.DNS.CredentialRef
	var dnsLease domain.DNSExecutionLease
	var guardedProvider *leasedDNSProvider
	var guardedAuthorization *leasedDNSAuthorization
	var lease *httpchallenge.Lease
	if independent {
		if challengeType != domain.ChallengeDNS01 || s.credentials == nil || s.providers == nil {
			return IssueResult{}, errors.New("独立 DNS 账户配置无效")
		}
		identity, ok := ctx.Value(dnsExecutionContextKey{}).(domain.DNSExecutionIdentity)
		if !ok {
			return IssueResult{}, errors.New("独立 DNS 账户缺少真实任务执行身份")
		}
		dnsLease, err = s.dnsLeases.AcquireDNSExecutionLease(ctx, item, identity)
		if err != nil {
			return IssueResult{}, errors.New("获取独立 DNS 账户执行租约失败，未使用系统凭据")
		}
		defer func() {
			if guardedAuthorization != nil && !guardedAuthorization.clean() {
				returnErr = errors.Join(returnErr, errors.New("DNS 授权探测清理尚未确认，执行租约及原凭据引用已保留"))
				return
			}
			if guardedProvider != nil && !guardedProvider.clean() {
				returnErr = errors.Join(returnErr, errors.New("DNS 挑战清理尚未确认，执行租约及原凭据引用已保留"))
				return
			}
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := s.dnsLeases.ReleaseDNSExecutionLease(finishCtx, dnsLease.ID); err != nil {
				returnErr = errors.Join(returnErr, errors.New("DNS 执行租约释放失败，原凭据引用已保留"))
			}
		}()
		providerID, credentialRef = dnsLease.Provider, dnsLease.CredentialRef
		if err := dnsprovider.ValidateAliyunTargetDomains(providerID, domains); err != nil {
			return IssueResult{}, err
		}
	} else if challengeType == domain.ChallengeHTTP01 {
		lease, err = s.httpChallenges.NewLease(ctx, domains)
		if err != nil {
			return IssueResult{}, err
		}
		defer lease.Close()
	} else {
		providerID, err = s.validateDNSIssueConfiguration(item, domains)
		if err != nil {
			return IssueResult{}, err
		}
	}
	if err := s.repository.UpdateCertificateState(ctx, item.ID, "issuing", item.ExpiresAt); err != nil {
		return IssueResult{}, fmt.Errorf("更新证书申请状态失败：%w", err)
	}
	fail := func(cause error) (IssueResult, error) {
		if stateErr := s.repository.UpdateCertificateState(context.Background(), item.ID, "failed", item.ExpiresAt); stateErr != nil {
			return IssueResult{}, fmt.Errorf("%v；更新失败状态时发生错误：%w", cause, stateErr)
		}
		return IssueResult{}, cause
	}

	var credentials map[string]string
	var provider challenge.Provider
	if lease != nil {
		provider = lease
		if err := ReportProgress(ctx, "certificate_issue", "正在通过 HTTP-01 申请证书，请确保全部域名的 80 端口挑战路径已接入"); err != nil {
			return fail(err)
		}
	} else {
		if independent {
			if err := s.dnsLeases.ValidateDNSExecutionLease(ctx, dnsLease); err != nil {
				return fail(errors.New("DNS 执行授权已失效，未读取凭据"))
			}
		}
		credentials, err = s.credentials.Get(ctx, credentialRef)
		if err != nil {
			return fail(fmt.Errorf("读取 DNS 凭据失败：%w", err))
		}
		if err := ReportProgress(ctx, "dns_validation", "正在验证 DNS 凭据权限"); err != nil {
			return fail(err)
		}
		checker := s.authorization
		if independent && checker != nil {
			guardedAuthorization = &leasedDNSAuthorization{checker: checker, store: s.dnsLeases, lease: dnsLease}
			checker = guardedAuthorization
		}
		if err := s.verifyDNSAuthorizationUsing(ctx, providerID, domains, credentials, checker); err != nil {
			return fail(err)
		}
		if err := ReportProgress(ctx, "certificate_issue", "DNS 凭据验证通过，正在申请证书"); err != nil {
			return fail(err)
		}
		provider, err = s.providers.Create(providerID, credentials)
		if err != nil {
			return fail(fmt.Errorf("创建 DNS Provider 失败：%w", err))
		}
		if independent {
			guardedProvider = &leasedDNSProvider{Provider: provider, store: s.dnsLeases, lease: dnsLease, pending: make(map[[3]string]bool)}
			provider = guardedProvider
		}
	}
	email := strings.TrimSpace(s.config.ACME.AccountEmail)
	if email == "" && len(s.config.Notification.Recipients) > 0 {
		email = strings.TrimSpace(s.config.Notification.Recipients[0])
	}
	user, err := s.accounts.LoadOrCreate(ctx, item.ACMEEnvironment, email)
	if err != nil {
		return fail(err)
	}
	issuer := s.newIssuer(user, provider, item.ACMEEnvironment)
	resource, issueErr := issuer.Obtain(ctx, ObtainRequest{
		Domains: domains, AccountEmail: email, Environment: item.ACMEEnvironment, PreferredChain: s.preferredChain, ChallengeType: challengeType,
	})
	if issueErr != nil && challengeType == domain.ChallengeDNS01 {
		s.invalidateDNSAuthorizationOnFailure(providerID, domains, credentials, issueErr)
	}
	if saveErr := s.accounts.Save(ctx, item.ACMEEnvironment, user); saveErr != nil {
		if issueErr != nil {
			return fail(fmt.Errorf("%v；保存 ACME 账户状态失败：%w", issueErr, saveErr))
		}
		return fail(saveErr)
	}
	if issueErr != nil {
		return fail(issueErr)
	}
	resource, err = ValidateResourceForDomains(resource, domains)
	if err != nil {
		return fail(err)
	}
	if err := s.validateACMEResource(ctx, resource, domains, item.ACMEEnvironment); err != nil {
		return fail(err)
	}
	saved, err := s.store.SaveForType(ctx, item.Name, string(item.CertificateType), resource)
	if err != nil {
		return fail(fmt.Errorf("保存证书文件失败：%w", err))
	}
	if err := s.repository.UpdateCertificateState(ctx, item.ID, "valid", &saved.NotAfter); err != nil {
		return IssueResult{}, fmt.Errorf("证书已保存，但更新域名状态失败：%w", err)
	}
	return IssueResult{DomainID: item.ID, Domains: domains, Saved: saved}, nil
}

// IssueForSetup 在初始化标记完成前申请并保存管理后台证书。
func (s *Service) IssueForSetup(ctx context.Context, request SetupCertificateRequest) (SavedResource, error) {
	if s == nil || s.credentials == nil || s.providers == nil || s.accounts == nil || s.newIssuer == nil {
		return SavedResource{}, errors.New("初始化证书申请服务未初始化")
	}
	domains, err := NormalizeDomains(request.Domains)
	if err != nil {
		return SavedResource{}, err
	}
	certificateType := strings.ToLower(strings.TrimSpace(request.CertificateType))
	if certificateType != "single" && certificateType != "wildcard" && certificateType != "san" {
		return SavedResource{}, fmt.Errorf("初始化证书类型无效：%s", certificateType)
	}
	providerID := strings.ToLower(strings.TrimSpace(request.ProviderID))
	credentialRef := strings.TrimSpace(request.CredentialRef)
	if providerID == "" || credentialRef == "" {
		return SavedResource{}, errors.New("初始化证书的 DNS Provider 或凭据引用未配置")
	}
	// 初始化流程同样必须先拒绝整单保护 SAN，不能触碰凭据或云端。
	if err := dnsprovider.ValidateAliyunTargetDomains(providerID, domains); err != nil {
		return SavedResource{}, err
	}
	store, err := managementFileStore(request.CertificateRoot, request.BackupRoot)
	if err != nil {
		return SavedResource{}, err
	}
	environment := strings.ToLower(strings.TrimSpace(request.Environment))
	if environment == "" {
		environment = s.config.ACME.Environment
	}
	if environment == "" {
		environment = "staging"
	}
	credentials, err := s.credentials.Get(ctx, credentialRef)
	if err != nil {
		return SavedResource{}, fmt.Errorf("读取初始化证书 DNS 凭据失败：%w", err)
	}
	if err := s.verifyDNSAuthorization(ctx, providerID, domains, credentials); err != nil {
		return SavedResource{}, err
	}
	provider, err := s.providers.Create(providerID, credentials)
	if err != nil {
		return SavedResource{}, fmt.Errorf("创建初始化证书 DNS Provider 失败：%w", err)
	}
	accountEmail := strings.TrimSpace(request.AccountEmail)
	if accountEmail == "" {
		accountEmail = strings.TrimSpace(s.config.ACME.AccountEmail)
	}
	user, err := s.accounts.LoadOrCreate(ctx, environment, accountEmail)
	if err != nil {
		return SavedResource{}, fmt.Errorf("读取 ACME 账户失败：%w", err)
	}
	issuer := s.newIssuer(user, provider, environment)
	resource, issueErr := issuer.Obtain(ctx, ObtainRequest{
		Domains: domains, AccountEmail: accountEmail, Environment: environment, PreferredChain: s.preferredChain,
	})
	if issueErr != nil {
		s.invalidateDNSAuthorizationOnFailure(providerID, domains, credentials, issueErr)
	}
	if saveErr := s.accounts.Save(ctx, environment, user); saveErr != nil {
		if issueErr != nil {
			return SavedResource{}, fmt.Errorf("%v；保存 ACME 账户状态失败：%w", issueErr, saveErr)
		}
		return SavedResource{}, fmt.Errorf("保存 ACME 账户状态失败：%w", saveErr)
	}
	if issueErr != nil {
		return SavedResource{}, issueErr
	}
	resource, err = ValidateResourceForDomains(resource, domains)
	if err != nil {
		return SavedResource{}, fmt.Errorf("初始化证书校验失败：%w", err)
	}
	if err := s.validateACMEResource(ctx, resource, domains, environment); err != nil {
		return SavedResource{}, err
	}
	saved, err := store.SaveForType(ctx, domains[0], certificateType, resource)
	if err != nil {
		return SavedResource{}, fmt.Errorf("保存初始化证书失败：%w", err)
	}
	return saved, nil
}

func (s *Service) verifyDNSAuthorization(ctx context.Context, providerID string, domains []string, credentials map[string]string) error {
	var checker DNSAuthorizationChecker
	if s != nil {
		checker = s.authorization
	}
	return s.verifyDNSAuthorizationUsing(ctx, providerID, domains, credentials, checker)
}

func (s *Service) verifyDNSAuthorizationUsing(ctx context.Context, providerID string, domains []string, credentials map[string]string, checker DNSAuthorizationChecker) error {
	// 必须先校验全部 SAN；后续域名越界时也不能先写入前面的域名。
	if err := dnsprovider.ValidateAliyunTargetDomains(providerID, domains); err != nil {
		return err
	}
	if checker == nil {
		if strings.EqualFold(strings.TrimSpace(providerID), dnsprovider.Aliyun) {
			return errors.New("阿里云权限探测器未初始化，已阻止证书申请")
		}
		return nil
	}
	for _, requestedDomain := range domains {
		if err := ReportProgress(ctx, "dns_validation", fmt.Sprintf("正在验证域名 %s 的 DNS 权限", requestedDomain)); err != nil {
			return err
		}
		if err := checker.VerifyDomain(ctx, providerID, requestedDomain, credentials); err != nil {
			return fmt.Errorf("DNS 凭据无权操作域名 %s：%w", requestedDomain, err)
		}
	}
	return nil
}

func (s *Service) invalidateDNSAuthorization(providerID string, domains []string, credentials map[string]string) {
	invalidator, ok := s.authorization.(DNSAuthorizationCacheInvalidator)
	if !ok {
		return
	}
	for _, requestedDomain := range domains {
		invalidator.InvalidateDomain(providerID, requestedDomain, credentials)
	}
}

func (s *Service) invalidateDNSAuthorizationOnFailure(providerID string, domains []string, credentials map[string]string, issueErr error) {
	if !looksLikeDNSAuthorizationFailure(issueErr) {
		return
	}
	s.invalidateDNSAuthorization(providerID, domains, credentials)
}

func looksLikeDNSAuthorizationFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"dns-01", "dns challenge", "txt record", "present", "cleanup", "clean up",
		"unauthorized", "forbidden", "permission", "accessdenied", "权限", "无权", "授权",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (s *Service) Import(ctx context.Context, request ImportRequest) (IssueResult, error) {
	if s == nil || s.repository == nil {
		return IssueResult{}, errors.New("证书导入服务未初始化")
	}
	item, err := s.repository.GetByID(ctx, strings.TrimSpace(request.DomainID))
	if err != nil {
		return IssueResult{}, fmt.Errorf("读取域名配置失败：%w", err)
	}
	certificatePath, err := cleanInputPath(request.Certificate, "证书文件")
	if err != nil {
		return IssueResult{}, err
	}
	privateKeyPath, err := cleanInputPath(request.PrivateKey, "私钥文件")
	if err != nil {
		return IssueResult{}, err
	}
	certificateData, err := os.ReadFile(certificatePath)
	if err != nil {
		return IssueResult{}, fmt.Errorf("读取证书文件失败：%w", err)
	}
	privateKeyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return IssueResult{}, fmt.Errorf("读取私钥文件失败：%w", err)
	}
	var issuerData []byte
	if strings.TrimSpace(request.IssuerBundle) != "" {
		issuerPath, err := cleanInputPath(request.IssuerBundle, "签发者证书文件")
		if err != nil {
			return IssueResult{}, err
		}
		issuerData, err = os.ReadFile(issuerPath)
		if err != nil {
			return IssueResult{}, fmt.Errorf("读取签发者证书文件失败：%w", err)
		}
	}
	domains := requestedDomainsForImport(item)
	resource, err := ValidateResourceForDomains(Resource{
		Domains: domains, Certificate: certificateData, IssuerCertificate: issuerData, PrivateKey: privateKeyData,
	}, domains)
	if err != nil {
		return IssueResult{}, err
	}
	if err := validateIssuedResource(ctx, resource, domains); err != nil {
		return IssueResult{}, err
	}
	saved, err := s.store.SaveForType(ctx, item.Name, string(item.CertificateType), resource)
	if err != nil {
		return IssueResult{}, fmt.Errorf("保存导入证书失败：%w", err)
	}
	if err := s.repository.UpdateCertificateState(ctx, item.ID, "valid", &saved.NotAfter); err != nil {
		return IssueResult{}, fmt.Errorf("证书已保存，但更新域名状态失败：%w", err)
	}
	return IssueResult{DomainID: item.ID, Domains: resource.Domains, Saved: saved}, nil
}

func validateIssuedResource(ctx context.Context, resource Resource, domains []string) error {
	chainData := append([]byte(nil), resource.Certificate...)
	chainData = append(chainData, resource.IssuerCertificate...)
	_, err := (LocalValidator{VerifyChain: len(resource.IssuerCertificate) > 0}).ValidatePEM(ctx, chainData, domains)
	if err != nil {
		return fmt.Errorf("本地证书校验失败：%w", err)
	}
	return nil
}

func cleanInputPath(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s不能为空", label)
	}
	path := filepath.Clean(value)
	if path == "." {
		return "", fmt.Errorf("%s路径无效", label)
	}
	return path, nil
}

func requestedDomainsForImport(item domain.Domain) []string {
	if item.CertificateType == domain.CertificateSAN && len(item.SANs) > 0 {
		return item.SANs
	}
	return []string{item.Name}
}

func requestedDomains(item domain.Domain) ([]string, error) {
	values := item.SANs
	if item.CertificateType != domain.CertificateSAN {
		values = []string{item.Name}
	}
	return NormalizeDomains(values)
}

// Current returns and validates the active certificate for a managed domain.
func (s *Service) Current(ctx context.Context, domainID string) (IssueResult, error) {
	if s == nil || s.repository == nil {
		return IssueResult{}, errors.New("当前证书服务未初始化")
	}
	item, err := s.repository.GetByID(ctx, strings.TrimSpace(domainID))
	if err != nil {
		return IssueResult{}, fmt.Errorf("读取域名配置失败：%w", err)
	}
	domains, err := requestedDomains(item)
	if err != nil {
		return IssueResult{}, err
	}
	saved, resource, err := s.store.LoadCurrent(ctx, item.Name, string(item.CertificateType))
	if err != nil {
		return IssueResult{}, err
	}
	if _, err := ValidateResourceForDomains(resource, domains); err != nil {
		return IssueResult{}, err
	}
	saved.SANs = append([]string(nil), resource.Domains...)
	return IssueResult{DomainID: item.ID, Domains: domains, Saved: saved}, nil
}

// ValidateCurrent 校验当前证书文件、私钥、证书链和域名覆盖范围。
func (s *Service) ValidateCurrent(ctx context.Context, domainID string) (ValidationResult, error) {
	result, err := s.Current(ctx, domainID)
	if err != nil {
		return ValidationResult{}, err
	}
	validation, err := (LocalValidator{VerifyChain: true}).ValidateFiles(
		ctx,
		result.Saved.CertificatePath,
		result.Saved.FullChainPath,
		result.Saved.PrivateKeyPath,
		result.Domains,
	)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("当前证书校验失败：%w", err)
	}
	return validation, nil
}

// ListVersions returns the certificate history for a managed domain.
func (s *Service) ListVersions(ctx context.Context, domainID string) ([]SavedResource, error) {
	if s == nil || s.repository == nil {
		return nil, errors.New("证书服务未初始化")
	}
	item, err := s.repository.GetByID(ctx, strings.TrimSpace(domainID))
	if err != nil {
		return nil, fmt.Errorf("读取域名配置失败：%w", err)
	}
	return s.store.ListVersions(ctx, item.Name, string(item.CertificateType))
}

// RestoreVersion 将指定历史版本恢复为当前证书，并更新域名证书状态。
func (s *Service) RestoreVersion(ctx context.Context, domainID, version string) (IssueResult, error) {
	if s == nil || s.repository == nil {
		return IssueResult{}, errors.New("证书服务未初始化")
	}
	item, err := s.repository.GetByID(ctx, strings.TrimSpace(domainID))
	if err != nil {
		return IssueResult{}, fmt.Errorf("读取域名配置失败：%w", err)
	}
	domains, err := requestedDomains(item)
	if err != nil {
		return IssueResult{}, err
	}
	_, resource, err := s.store.LoadVersion(ctx, item.Name, string(item.CertificateType), strings.TrimSpace(version))
	if err != nil {
		return IssueResult{}, err
	}
	if _, err := ValidateResourceForDomains(resource, domains); err != nil {
		return IssueResult{}, fmt.Errorf("历史证书不匹配当前域名配置：%w", err)
	}
	saved, err := s.store.restoreResource(ctx, item.Name, string(item.CertificateType), resource, domains, func(saved SavedResource) error {
		return s.repository.UpdateCertificateState(ctx, item.ID, "valid", &saved.NotAfter)
	})
	if err != nil {
		return IssueResult{}, err
	}
	saved.SANs = append([]string(nil), domains...)
	return IssueResult{DomainID: item.ID, Domains: domains, Saved: saved}, nil
}
