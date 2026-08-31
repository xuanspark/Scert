package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/notification"
)

type CertificateWorkflow struct {
	Certificates               certificateService
	Domains                    domainDeploymentRepository
	Servers                    serverLister
	Deployment                 savedDeployer
	Notifier                   notification.Notifier
	NotifyError                func(error)
	RemoteVerifier             func(context.Context, string, []string) (certificate.ValidationResult, error)
	RemoteVerificationAttempts int
	RemoteVerificationInterval time.Duration
	RemoteVerificationWait     func(context.Context, time.Duration) error
	CurrentCertificate         func(context.Context, string) (certificate.IssueResult, error)
}

const (
	defaultRemoteVerificationAttempts = 6
	defaultRemoteVerificationInterval = 500 * time.Millisecond
)

type certificateService interface {
	Issue(context.Context, string) (certificate.IssueResult, error)
	Import(context.Context, certificate.ImportRequest) (certificate.IssueResult, error)
}

type serverLister interface {
	ListServersByDomainID(context.Context, string) ([]deployment.Server, error)
}

type savedDeployer interface {
	DeploySaved(context.Context, []deployment.Target, deployment.SavedFiles) (deployment.Report, error)
}

type domainDeploymentRepository interface {
	GetByID(context.Context, string) (domain.Domain, error)
	UpdateCertificateState(context.Context, string, string, *time.Time) error
}

type CertificateWorkflowResult struct {
	Certificate certificate.IssueResult `json:"certificate"`
	Deployment  deployment.Report       `json:"deployment"`
}

func (w *CertificateWorkflow) Issue(ctx context.Context, domainID string) (certificate.IssueResult, error) {
	result, err := w.IssueWithReport(ctx, domainID)
	return result.Certificate, err
}

func (w *CertificateWorkflow) IssueWithReport(ctx context.Context, domainID string) (CertificateWorkflowResult, error) {
	if w == nil || w.Certificates == nil || w.Domains == nil || w.Servers == nil || w.Deployment == nil {
		return CertificateWorkflowResult{}, errors.New("证书部署流程未初始化")
	}
	domainItem, _ := w.Domains.GetByID(ctx, domainID)
	certificateResult, err := w.Certificates.Issue(ctx, domainID)
	if err != nil {
		w.notify(ctx, notification.Event{Type: "certificate_renewal", Status: "failed", DomainID: domainID, Domain: domainItem.Name, Error: err.Error()})
		return CertificateWorkflowResult{}, err
	}
	report, err := w.deploy(ctx, certificateResult)
	if err != nil {
		w.markDeploymentError(certificateResult)
	}
	status := "success"
	if err != nil {
		status = "error"
	}
	w.notify(ctx, notification.Event{
		Type: "certificate_renewal", Status: status, DomainID: certificateResult.DomainID,
		Domain: domainItem.Name, Domains: certificateResult.Domains, ExpiresAt: &certificateResult.Saved.NotAfter,
		Deployment: report, Error: errorText(err),
	})
	return CertificateWorkflowResult{Certificate: certificateResult, Deployment: report}, err
}

func (w *CertificateWorkflow) IssueForHTTP(ctx context.Context, domainID string) (certificate.IssueResult, deployment.Report, error) {
	result, err := w.IssueWithReport(ctx, domainID)
	return result.Certificate, result.Deployment, err
}

// DeployCurrent validates and deploys the active certificate without issuing a new one.
func (w *CertificateWorkflow) DeployCurrent(ctx context.Context, domainID string) (CertificateWorkflowResult, error) {
	if w == nil || w.CurrentCertificate == nil || w.Domains == nil || w.Servers == nil || w.Deployment == nil {
		return CertificateWorkflowResult{}, errors.New("当前证书部署流程未初始化")
	}
	result, err := w.CurrentCertificate(ctx, domainID)
	if err != nil {
		return CertificateWorkflowResult{}, err
	}
	report, err := w.deploy(ctx, result)
	if err != nil {
		w.markDeploymentError(result)
	} else if updateErr := w.Domains.UpdateCertificateState(ctx, result.DomainID, "valid", &result.Saved.NotAfter); updateErr != nil {
		err = fmt.Errorf("证书部署成功，但更新域名证书状态失败：%w", updateErr)
	}
	status := "success"
	if err != nil {
		status = "error"
	}
	w.notify(ctx, notification.Event{
		Type: "certificate_deploy", Status: status, DomainID: result.DomainID,
		Domains: result.Domains, ExpiresAt: &result.Saved.NotAfter, Deployment: report, Error: errorText(err),
	})
	return CertificateWorkflowResult{Certificate: result, Deployment: report}, err
}
func (w *CertificateWorkflow) Import(ctx context.Context, request certificate.ImportRequest) (CertificateWorkflowResult, error) {
	if w == nil || w.Certificates == nil || w.Domains == nil || w.Servers == nil || w.Deployment == nil {
		return CertificateWorkflowResult{}, errors.New("证书部署流程未初始化")
	}
	domainItem, _ := w.Domains.GetByID(ctx, request.DomainID)
	certificateResult, err := w.Certificates.Import(ctx, request)
	if err != nil {
		w.notify(ctx, notification.Event{Type: "certificate_import", Status: "failed", DomainID: request.DomainID, Domain: domainItem.Name, Error: err.Error()})
		return CertificateWorkflowResult{}, err
	}
	report, err := w.deploy(ctx, certificateResult)
	if err != nil {
		w.markDeploymentError(certificateResult)
	}
	status := "success"
	if err != nil {
		status = "error"
	}
	w.notify(ctx, notification.Event{
		Type: "certificate_import", Status: status, DomainID: certificateResult.DomainID,
		Domain: domainItem.Name, Domains: certificateResult.Domains, ExpiresAt: &certificateResult.Saved.NotAfter,
		Deployment: report, Error: errorText(err),
	})
	return CertificateWorkflowResult{Certificate: certificateResult, Deployment: report}, err
}

func (w *CertificateWorkflow) notify(ctx context.Context, event notification.Event) {
	if w == nil || w.Notifier == nil {
		return
	}
	if err := w.Notifier.Send(ctx, event); err != nil && w.NotifyError != nil {
		w.NotifyError(err)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (w *CertificateWorkflow) deploy(ctx context.Context, result certificate.IssueResult) (deployment.Report, error) {
	domainItem, err := w.Domains.GetByID(ctx, result.DomainID)
	if err != nil {
		return deployment.Report{}, fmt.Errorf("查询域名配置失败：%w", err)
	}
	servers, err := w.Servers.ListServersByDomainID(ctx, result.DomainID)
	if err != nil {
		return deployment.Report{}, fmt.Errorf("查询域名部署服务器失败：%w", err)
	}
	targets := make([]deployment.Target, 0, len(servers))
	for _, server := range servers {
		resolved, resolveErr := deployment.ResolveServerPaths(server, domainItem.Name, string(domainItem.CertificateType))
		if resolveErr != nil {
			return deployment.Report{}, fmt.Errorf("解析服务器 %s 的证书路径失败：%w", server.Name, resolveErr)
		}
		targets = append(targets, deployment.Target{Server: resolved})
	}
	report, err := w.Deployment.DeploySaved(ctx, targets, deployment.SavedFiles{
		CertificatePath: result.Saved.CertificatePath,
		FullChainPath:   result.Saved.FullChainPath,
		PrivateKeyPath:  result.Saved.PrivateKeyPath,
	})
	if err != nil {
		return report, err
	}
	if report.Status != "success" {
		return report, fmt.Errorf("证书部署未全部成功：成功 %d 台，失败 %d 台", report.Success, report.Failed)
	}
	if w.RemoteVerifier != nil {
		verificationDomain := verificationName(domainItem, result.Domains)
		if verificationDomain == "" {
			return report, errors.New("无法确定部署后的 HTTPS 校验域名")
		}
		if err := w.verifyRemoteDeployment(ctx, "https://"+verificationDomain, result.Domains); err != nil {
			return report, fmt.Errorf("部署后 HTTPS 校验失败：%w", err)
		}
	}
	return report, nil
}

func (w *CertificateWorkflow) verifyRemoteDeployment(ctx context.Context, endpoint string, expectedDomains []string) error {
	attempts := w.RemoteVerificationAttempts
	if attempts <= 0 {
		attempts = defaultRemoteVerificationAttempts
	}
	interval := w.RemoteVerificationInterval
	if interval <= 0 {
		interval = defaultRemoteVerificationInterval
	}
	wait := w.RemoteVerificationWait
	if wait == nil {
		wait = waitForRemoteVerification
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := w.RemoteVerifier(ctx, endpoint, expectedDomains); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == attempts {
			break
		}
		if err := wait(ctx, interval); err != nil {
			return fmt.Errorf("等待 HTTPS 校验重试时被中断：%w", err)
		}
	}
	return fmt.Errorf("重试 %d 次后仍未通过：%w", attempts, lastErr)
}

func waitForRemoteVerification(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func verificationName(item domain.Domain, issuedDomains []string) string {
	for _, value := range issuedDomains {
		value = strings.TrimSpace(value)
		if value != "" && !strings.HasPrefix(value, "*.") {
			return value
		}
	}
	return strings.TrimPrefix(strings.TrimSpace(item.Name), "*.")
}

func (w *CertificateWorkflow) markDeploymentError(result certificate.IssueResult) {
	if w == nil || w.Domains == nil {
		return
	}
	_ = w.Domains.UpdateCertificateState(context.Background(), result.DomainID, "error", &result.Saved.NotAfter)
}
