package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type initCertificateIssuer func(context.Context, config.Config) (certificate.SavedResource, error)
type initCertificateRollback func(context.Context, certificate.SavedResource) error

func issueInitCertificate(ctx context.Context, cfg config.Config) (certificate.SavedResource, error) {
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		return certificate.SavedResource{}, fmt.Errorf("打开初始化凭据存储失败：%w", err)
	}
	service := certificate.NewService(cfg, nil, store, dnsprovider.NewLegoFactory(), certificate.NewAccountRepository(store))
	service.SetDNSAuthorizationChecker(dnsprovider.NewAliyunAuthorizationChecker())
	return service.IssueForSetup(ctx, certificate.SetupCertificateRequest{
		Domains: []string{cfg.Web.Hostname}, CertificateType: "single",
		ProviderID: cfg.DNS.Provider, CredentialRef: cfg.DNS.CredentialRef,
		AccountEmail: cfg.Admin.Email, Environment: cfg.ACME.Environment,
		CertificateRoot: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup,
	})
}

// completeInit 在证书校验及配置保存均成功后才发布就绪状态；失败时独立回滚证书。
func completeInit(ctx context.Context, cfg *config.Config, path string, manager firewall.Manager, issue initCertificateIssuer, rollback initCertificateRollback) (result firewall.Result, err error) {
	if cfg == nil || issue == nil || rollback == nil {
		return result, errors.New("初始化证书事务未配置")
	}
	cfg.SetupReady, cfg.Web.InitReady = false, false
	if cfg.Web.PublicIP != "" || strings.TrimSpace(cfg.Web.Hostname) == "" {
		return result, errors.New("DNS-01 初始化必须配置管理后台域名，不支持直接签发公网 IP 证书")
	}
	if err := cfg.Validate(); err != nil {
		return result, err
	}
	issueCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	saved, err := issue(issueCtx, *cfg)
	cancel()
	if err != nil {
		return result, fmt.Errorf("申请管理后台证书失败，初始化尚未完成：%w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if rollbackErr := rollback(rollbackCtx, saved); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("管理后台证书回滚失败，请手动检查证书目录：%w", rollbackErr))
		}
	}()
	for _, filename := range []string{saved.CertificatePath, saved.FullChainPath, saved.PrivateKeyPath} {
		info, statErr := os.Lstat(filename)
		if statErr != nil || !info.Mode().IsRegular() {
			return result, fmt.Errorf("初始化证书必须是可读取的普通文件：%s", filename)
		}
	}
	if _, err = (certificate.LocalValidator{}).ValidateFiles(ctx, saved.CertificatePath, saved.FullChainPath, saved.PrivateKeyPath, []string{cfg.Web.Hostname}); err != nil {
		return result, fmt.Errorf("初始化证书文件校验失败：%w", err)
	}
	// 签发服务已按 ACME 环境验证信任链，这里再次校验实际落盘内容与私钥。
	updated := *cfg
	updated.Server.TLSCert, updated.Server.TLSKey = saved.FullChainPath, saved.PrivateKeyPath
	updated.SetupReady, updated.Web.InitReady = true, true
	if err = updated.Validate(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	result, err = saveSetupWithFirewall(ctx, updated, path, manager)
	if err == nil {
		*cfg = updated
	}
	return result, err
}

func verifyExistingDNSCredentials(ctx context.Context, cfg config.Config, input *setup.Input, output io.Writer, checker certificate.DNSAuthorizationChecker) (func(), error) {
	input.DNSVerified = false
	if !strings.EqualFold(input.DNSProvider, dnsprovider.Aliyun) || checker == nil {
		return func() {}, errors.New("已有凭据引用必须通过阿里云在线权限校验，不能通过参数声明已验证")
	}
	target := strings.TrimSpace(input.WebHostname)
	if target == "" {
		return func() {}, errors.New("校验已有 DNS 凭据前必须填写管理后台域名")
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		return func() {}, err
	}
	credentials, err := store.Get(ctx, input.DNSCredentialRef)
	if err != nil {
		return func() {}, fmt.Errorf("读取已有 DNS 凭据失败：%w", err)
	}
	verifyCtx, cancel := context.WithTimeout(ctx, dnsVerificationTimeout(cfg))
	defer cancel()
	if err := checker.VerifyDomain(verifyCtx, input.DNSProvider, target, credentials); err != nil {
		return func() {}, fmt.Errorf("已有 DNS 凭据在线权限校验失败：%w", err)
	}
	input.DNSVerified = true
	fmt.Fprintln(output, "已有 DNS 凭据已重新通过管理后台域名的在线权限校验。")
	// 用户原有凭据不属于本次事务，即使后续失败也不得删除。
	return func() {}, nil
}
