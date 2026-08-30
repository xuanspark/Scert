package certificate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
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
	accounts       AccountManager
	store          FileStore
	newIssuer      IssuerFactory
	preferredChain string
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
		store: FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup},
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

func (s *Service) Issue(ctx context.Context, domainID string) (IssueResult, error) {
	if s == nil || s.repository == nil || s.credentials == nil || s.providers == nil || s.accounts == nil || s.newIssuer == nil {
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
	domains, err := requestedDomains(item)
	if err != nil {
		return IssueResult{}, err
	}
	providerID := strings.ToLower(strings.TrimSpace(item.DNSProvider))
	if providerID == "" {
		providerID = strings.ToLower(strings.TrimSpace(s.config.DNS.Provider))
	}
	if providerID == "" || strings.TrimSpace(s.config.DNS.CredentialRef) == "" {
		return IssueResult{}, errors.New("DNS Provider 或凭据引用未配置")
	}
	if configured := strings.ToLower(strings.TrimSpace(s.config.DNS.Provider)); configured != "" && providerID != configured {
		return IssueResult{}, fmt.Errorf("域名配置的 DNS Provider %s 尚未绑定独立凭据，当前初始化凭据属于 %s", providerID, configured)
	}
	if s.config.DNS.VerificationState != "verified" {
		return IssueResult{}, errors.New("DNS API 尚未通过验证")
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

	credentials, err := s.credentials.Get(ctx, s.config.DNS.CredentialRef)
	if err != nil {
		return fail(fmt.Errorf("读取 DNS 凭据失败：%w", err))
	}
	provider, err := s.providers.Create(providerID, credentials)
	if err != nil {
		return fail(fmt.Errorf("创建 DNS Provider 失败：%w", err))
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
		Domains: domains, AccountEmail: email, Environment: item.ACMEEnvironment, PreferredChain: s.preferredChain,
	})
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
	if err := validateIssuedResource(ctx, resource, domains); err != nil {
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
	if err := validateIssuedResource(ctx, resource, domains); err != nil {
		return SavedResource{}, err
	}
	store := FileStore{Root: request.CertificateRoot, BackupRoot: request.BackupRoot}
	saved, err := store.SaveForType(ctx, domains[0], certificateType, resource)
	if err != nil {
		return SavedResource{}, fmt.Errorf("保存初始化证书失败：%w", err)
	}
	return saved, nil
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
	saved, err := s.store.RestoreVersion(ctx, item.Name, string(item.CertificateType), strings.TrimSpace(version))
	if err != nil {
		return IssueResult{}, err
	}
	if _, err := ValidateResourceForDomains(resource, domains); err != nil {
		return IssueResult{}, fmt.Errorf("恢复后证书校验失败：%w", err)
	}
	if err := s.repository.UpdateCertificateState(ctx, item.ID, "valid", &saved.NotAfter); err != nil {
		rollbackErr := RollbackSavedResource(context.Background(), saved)
		if rollbackErr != nil {
			return IssueResult{}, errors.Join(err, rollbackErr)
		}
		return IssueResult{}, fmt.Errorf("certificate state update failed after restore: %w", err)
	}
	saved.SANs = append([]string(nil), domains...)
	return IssueResult{DomainID: item.ID, Domains: domains, Saved: saved}, nil
}
