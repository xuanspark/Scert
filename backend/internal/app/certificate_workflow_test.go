package app

import (
	"context"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type workflowCertificateService struct {
	result certificate.IssueResult
	called bool
}

func (s *workflowCertificateService) Issue(context.Context, string) (certificate.IssueResult, error) {
	s.called = true
	return s.result, nil
}

func (s *workflowCertificateService) Import(context.Context, certificate.ImportRequest) (certificate.IssueResult, error) {
	s.called = true
	return s.result, nil
}

type workflowServerLister struct {
	servers []deployment.Server
}

func (s workflowServerLister) ListServersByDomainID(context.Context, string) ([]deployment.Server, error) {
	return s.servers, nil
}

type workflowDeployer struct {
	targets []deployment.Target
	files   deployment.SavedFiles
	report  deployment.Report
}

type workflowDomainState struct {
	id     string
	status string
	item   domain.Domain
}

func (s *workflowDomainState) GetByID(_ context.Context, id string) (domain.Domain, error) {
	item := s.item
	if item.ID == "" {
		item.ID = id
	}
	if item.Name == "" {
		item.Name = "example.com"
	}
	if item.CertificateType == "" {
		item.CertificateType = domain.CertificateSingle
	}
	return item, nil
}

func (s *workflowDomainState) UpdateCertificateState(_ context.Context, id, status string, _ *time.Time) error {
	s.id, s.status = id, status
	return nil
}

func (d *workflowDeployer) DeploySaved(_ context.Context, targets []deployment.Target, files deployment.SavedFiles) (deployment.Report, error) {
	d.targets = targets
	d.files = files
	return d.report, nil
}

func TestCertificateWorkflowDeploysIssuedCertificateToAssociatedServers(t *testing.T) {
	certificateService := &workflowCertificateService{result: certificate.IssueResult{
		DomainID: "domain-1",
		Saved:    certificate.SavedResource{CertificatePath: "cert.pem", FullChainPath: "fullchain.pem", PrivateKeyPath: "key.pem"},
	}}
	deployer := &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle}}
	workflow := &CertificateWorkflow{
		Certificates: certificateService,
		Domains:      state,
		Servers:      workflowServerLister{servers: []deployment.Server{{ID: "server-1", Name: "生产服务器"}}},
		Deployment:   deployer,
	}
	result, err := workflow.IssueWithReport(context.Background(), "domain-1")
	if err != nil {
		t.Fatalf("证书工作流失败：%v", err)
	}
	if !certificateService.called || result.Deployment.Status != "success" || len(deployer.targets) != 1 {
		t.Fatalf("证书工作流结果错误：%+v %+v", result, deployer.targets)
	}
	if deployer.files.PrivateKeyPath != "key.pem" {
		t.Fatalf("部署文件路径错误：%+v", deployer.files)
	}
	if deployer.targets[0].Server.CertificatePath != "/ssl/example.com/single/cert.pem" || deployer.targets[0].Server.PrivateKeyPath != "/ssl/example.com/single/key.pem" {
		t.Fatalf("部署目标路径未按域名和证书类型解析：%+v", deployer.targets[0].Server)
	}
}

func TestCertificateWorkflowReturnsPartialDeploymentError(t *testing.T) {
	deployer := &workflowDeployer{report: deployment.Report{Total: 2, Success: 1, Failed: 1, Status: "partial"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "*.example.com", CertificateType: domain.CertificateWildcard}}
	workflow := &CertificateWorkflow{
		Certificates: &workflowCertificateService{result: certificate.IssueResult{DomainID: "domain-1"}},
		Domains:      state,
		Servers:      workflowServerLister{servers: []deployment.Server{{ID: "server-1"}, {ID: "server-2"}}},
		Deployment:   deployer,
	}
	result, err := workflow.IssueWithReport(context.Background(), "domain-1")
	if err == nil || result.Deployment.Status != "partial" {
		t.Fatalf("部分部署应返回错误和报告：%+v %v", result, err)
	}
	if result.Certificate.DomainID != "domain-1" {
		t.Fatalf("证书结果未保留：%+v", result.Certificate)
	}
	if state.id != "domain-1" || state.status != "error" {
		t.Fatalf("部署失败后域名状态未标记为错误：%+v", state)
	}
}

func TestCertificateWorkflowResolvesSameServerForDifferentDomains(t *testing.T) {
	server := deployment.Server{ID: "server-1", Name: "共享服务器"}
	deployer := &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSAN}}
	workflow := &CertificateWorkflow{
		Certificates: &workflowCertificateService{result: certificate.IssueResult{DomainID: "domain-1"}},
		Domains:      state,
		Servers:      workflowServerLister{servers: []deployment.Server{server}},
		Deployment:   deployer,
	}
	if _, err := workflow.IssueWithReport(context.Background(), "domain-1"); err != nil {
		t.Fatalf("第一个域名部署失败：%v", err)
	}
	firstPath := deployer.targets[0].Server.CertificatePath
	state.item = domain.Domain{ID: "domain-2", Name: "api.example.net", CertificateType: domain.CertificateSingle}
	workflow.Certificates = &workflowCertificateService{result: certificate.IssueResult{DomainID: "domain-2"}}
	if _, err := workflow.IssueWithReport(context.Background(), "domain-2"); err != nil {
		t.Fatalf("第二个域名部署失败：%v", err)
	}
	secondPath := deployer.targets[0].Server.CertificatePath
	if firstPath != "/ssl/example.com/san/cert.pem" || secondPath != "/ssl/api.example.net/single/cert.pem" || firstPath == secondPath {
		t.Fatalf("共享服务器的域名部署路径发生覆盖：%s %s", firstPath, secondPath)
	}
}
