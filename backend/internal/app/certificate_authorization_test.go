package app

import (
	"context"
	"errors"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type deniedWorkflowCertificateService struct {
	workflowCertificateService
}

func (*deniedWorkflowCertificateService) Issue(context.Context, string) (certificate.IssueResult, error) {
	return certificate.IssueResult{}, errors.New("DNS 凭据无权操作域名：Forbidden.NoPermission")
}

type forbiddenDeployer struct {
	t *testing.T
}

func (d forbiddenDeployer) DeploySaved(context.Context, []deployment.Target, deployment.SavedFiles) (deployment.Report, error) {
	d.t.Fatal("权限检查失败后不得发起 SSH/SFTP 部署")
	return deployment.Report{}, nil
}

func TestWorkflowDoesNotDeployAfterDNSPermissionDenied(t *testing.T) {
	workflow := &CertificateWorkflow{
		Certificates: &deniedWorkflowCertificateService{},
		Domains:      &workflowDomainState{},
		Servers:      workflowServerLister{servers: []deployment.Server{{ID: "target"}}},
		Deployment:   forbiddenDeployer{t: t},
	}
	if _, err := workflow.IssueWithReport(t.Context(), "domain"); err == nil {
		t.Fatal("权限失败不得报告工作流成功")
	}
}
