package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type CertificateWorkflow struct {
	Certificates certificateService
	Domains      domainDeploymentRepository
	Servers      serverLister
	Deployment   savedDeployer
}

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
	certificateResult, err := w.Certificates.Issue(ctx, domainID)
	if err != nil {
		return CertificateWorkflowResult{}, err
	}
	report, err := w.deploy(ctx, certificateResult)
	if err != nil {
		w.markDeploymentError(certificateResult)
	}
	return CertificateWorkflowResult{Certificate: certificateResult, Deployment: report}, err
}

func (w *CertificateWorkflow) IssueForHTTP(ctx context.Context, domainID string) (certificate.IssueResult, deployment.Report, error) {
	result, err := w.IssueWithReport(ctx, domainID)
	return result.Certificate, result.Deployment, err
}

func (w *CertificateWorkflow) Import(ctx context.Context, request certificate.ImportRequest) (CertificateWorkflowResult, error) {
	if w == nil || w.Certificates == nil || w.Domains == nil || w.Servers == nil || w.Deployment == nil {
		return CertificateWorkflowResult{}, errors.New("证书部署流程未初始化")
	}
	certificateResult, err := w.Certificates.Import(ctx, request)
	if err != nil {
		return CertificateWorkflowResult{}, err
	}
	report, err := w.deploy(ctx, certificateResult)
	if err != nil {
		w.markDeploymentError(certificateResult)
	}
	return CertificateWorkflowResult{Certificate: certificateResult, Deployment: report}, err
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
	return report, nil
}

func (w *CertificateWorkflow) markDeploymentError(result certificate.IssueResult) {
	if w == nil || w.Domains == nil {
		return
	}
	_ = w.Domains.UpdateCertificateState(context.Background(), result.DomainID, "error", &result.Saved.NotAfter)
}
