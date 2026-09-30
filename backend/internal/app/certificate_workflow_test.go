package app

import (
	"context"
	"errors"
	"strings"
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
	err     error
}

func (s workflowServerLister) ListDeploymentServersByDomainID(context.Context, string) ([]deployment.Server, error) {
	return s.servers, s.err
}

func TestCertificateWorkflowStopsBeforeRemoteWriteOnSharedPathConflict(t *testing.T) {
	deployer := &workflowDeployer{}
	workflow := &CertificateWorkflow{
		Domains:    &workflowDomainState{},
		Servers:    workflowServerLister{err: deployment.ErrSharedPathConflict},
		Deployment: deployer,
	}
	_, err := workflow.deploy(t.Context(), certificate.IssueResult{DomainID: "shared-domain"})
	if !errors.Is(err, deployment.ErrSharedPathConflict) || deployer.targets != nil {
		t.Fatalf("路径冲突后仍尝试远端部署：%v，%+v", err, deployer.targets)
	}
}

type workflowDeployer struct {
	targets []deployment.Target
	files   deployment.SavedFiles
	report  deployment.Report
}

type workflowDomainState struct {
	id           string
	status       string
	expiresAt    *time.Time
	updateErr    error
	updateCalled int
	item         domain.Domain
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

func (s *workflowDomainState) UpdateCertificateState(_ context.Context, id, status string, expiresAt *time.Time) error {
	s.updateCalled++
	if s.updateErr != nil {
		return s.updateErr
	}
	s.id, s.status = id, status
	if expiresAt != nil {
		value := *expiresAt
		s.expiresAt = &value
	}
	return nil
}

func (d *workflowDeployer) DeploySaved(_ context.Context, targets []deployment.Target, files deployment.SavedFiles) (deployment.Report, error) {
	d.targets = targets
	d.files = files
	return d.report, nil
}

// 工作流替身只模拟推送和校验，不伪造远端事务恢复；恢复边界由部署包实测。
func (d *workflowDeployer) DeploySavedVerified(ctx context.Context, targets []deployment.Target, files deployment.SavedFiles, verification deployment.Verification) (deployment.Report, error) {
	report, err := d.DeploySaved(ctx, targets, files)
	if err != nil || report.Status != "success" {
		return report, err
	}
	report.VerificationStatus = "failed"
	if err := verification.Verify(ctx); err != nil {
		report.Status = "failed"
		return report, err
	}
	report.VerificationStatus = "success"
	return report, nil
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

func TestCertificateWorkflowRejectsDomainWithoutDeploymentServers(t *testing.T) {
	deployer := &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle}}
	workflow := &CertificateWorkflow{
		Domains:    state,
		Servers:    workflowServerLister{},
		Deployment: deployer,
	}

	report, err := workflow.deploy(context.Background(), certificate.IssueResult{DomainID: "domain-1"})
	if err == nil || !strings.Contains(err.Error(), "\u5c1a\u672a\u5173\u8054\u4efb\u4f55\u90e8\u7f72\u670d\u52a1\u5668") {
		t.Fatalf("\u672a\u5173\u8054\u670d\u52a1\u5668\u65f6\u5e94\u8fd4\u56de\u660e\u786e\u9519\u8bef\xef\xbc\x9a%v", err)
	}
	if report.Status != "failed" || deployer.targets != nil {
		t.Fatalf("\u672a\u5173\u8054\u670d\u52a1\u5668\u65f6\u4e0d\u5f97\u6267\u884c\u8fdc\u7a0b\u90e8\u7f72\xef\xbc\x9a%+v %+v", report, deployer.targets)
	}
}

func TestCertificateWorkflowReturnsPartialDeploymentError(t *testing.T) {
	deployer := &workflowDeployer{report: deployment.Report{
		Total: 2, Success: 1, Failed: 1, Status: "partial",
		Results: []deployment.Result{
			{ServerID: "server-1", Server: "primary", Status: "success", Message: "deployed"},
			{ServerID: "server-2", Server: "secondary", Status: "failed", Message: "validation failed"},
		},
	}}
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
	if !strings.Contains(err.Error(), "secondary: validation failed") || strings.Contains(err.Error(), "primary: deployed") {
		t.Fatalf("deployment error details are incomplete: %v", err)
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

func TestCertificateWorkflowRetriesRemoteVerificationUntilSuccess(t *testing.T) {
	saved := workflowSavedCertificate(t)
	deployer := &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle}}
	verificationCalls := 0
	waitCalls := 0
	workflow := &CertificateWorkflow{
		Certificates:               &workflowCertificateService{result: certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved}},
		Domains:                    state,
		Servers:                    workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment:                 deployer,
		RemoteVerificationAttempts: 4,
		RemoteVerificationInterval: 10 * time.Millisecond,
		RemoteVerificationWait: func(ctx context.Context, interval time.Duration) error {
			waitCalls++
			if interval != 10*time.Millisecond {
				t.Fatalf("HTTPS 校验重试间隔错误：%s", interval)
			}
			return ctx.Err()
		},
		RemoteVerifier: func(_ context.Context, endpoint string, expectedDomains []string) (certificate.ValidationResult, error) {
			verificationCalls++
			if endpoint != "https://example.com" || len(expectedDomains) != 1 || expectedDomains[0] != "example.com" {
				t.Fatalf("HTTPS 校验参数错误：%s %+v", endpoint, expectedDomains)
			}
			if verificationCalls < 4 {
				return certificate.ValidationResult{}, errors.New("仍返回旧证书")
			}
			return certificate.ValidationResult{FingerprintSHA256: saved.FingerprintSHA256}, nil
		},
	}

	if _, err := workflow.IssueWithReport(context.Background(), "domain-1"); err != nil {
		t.Fatalf("HTTPS 校验重试后应成功：%v", err)
	}
	if verificationCalls != 4 || waitCalls != 2 {
		t.Fatalf("HTTPS 校验重试次数错误：校验 %d 次，等待 %d 次", verificationCalls, waitCalls)
	}
}

func TestCertificateWorkflowRemoteVerificationStopsAfterConfiguredAttempts(t *testing.T) {
	saved := workflowSavedCertificate(t)
	deployer := &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle}}
	verificationCalls := 0
	waitCalls := 0
	workflow := &CertificateWorkflow{
		Certificates:               &workflowCertificateService{result: certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved}},
		Domains:                    state,
		Servers:                    workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment:                 deployer,
		RemoteVerificationAttempts: 3,
		RemoteVerificationWait: func(context.Context, time.Duration) error {
			waitCalls++
			return nil
		},
		RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
			verificationCalls++
			return certificate.ValidationResult{}, errors.New("证书永久不匹配")
		},
	}

	_, err := workflow.IssueWithReport(context.Background(), "domain-1")
	if err == nil || !strings.Contains(err.Error(), "重试 3 次后仍未通过") {
		t.Fatalf("永久校验错误应在有限重试后返回：%v", err)
	}
	if verificationCalls != 4 || waitCalls != 2 {
		t.Fatalf("永久错误的重试次数错误：校验 %d 次，等待 %d 次", verificationCalls, waitCalls)
	}
}

func TestCertificateWorkflowRemoteVerificationWaitHonorsContextCancellation(t *testing.T) {
	saved := workflowSavedCertificate(t)
	deployer := &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}}
	state := &workflowDomainState{item: domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle}}
	ctx, cancel := context.WithCancel(context.Background())
	verificationCalls := 0
	workflow := &CertificateWorkflow{
		Certificates:               &workflowCertificateService{result: certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved}},
		Domains:                    state,
		Servers:                    workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment:                 deployer,
		RemoteVerificationAttempts: 6,
		RemoteVerificationInterval: time.Minute,
		RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
			verificationCalls++
			if verificationCalls == 1 {
				return certificate.ValidationResult{FingerprintSHA256: saved.FingerprintSHA256}, nil
			}
			cancel()
			return certificate.ValidationResult{}, errors.New("等待新证书")
		},
	}

	_, err := workflow.IssueWithReport(ctx, "domain-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消上下文后应立即终止 HTTPS 校验重试：%v", err)
	}
	if verificationCalls != 2 {
		t.Fatalf("取消后不应继续校验，实际调用 %d 次", verificationCalls)
	}
}

func TestCertificateWorkflowDeployCurrentRestoresValidState(t *testing.T) {
	notAfter := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	saved := workflowSavedCertificate(t)
	saved.NotAfter = notAfter
	state := &workflowDomainState{
		status: "error",
		item:   domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle},
	}
	workflow := &CertificateWorkflow{
		CurrentCertificate: func(context.Context, string) (certificate.IssueResult, error) {
			return certificate.IssueResult{
				DomainID: "domain-1",
				Domains:  []string{"example.com"},
				Saved:    saved,
			}, nil
		},
		Domains:    state,
		Servers:    workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment: &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
		RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
			return certificate.ValidationResult{FingerprintSHA256: saved.FingerprintSHA256}, nil
		},
	}

	result, err := workflow.DeployCurrent(context.Background(), "domain-1")
	if err != nil {
		t.Fatalf("当前证书重新部署应成功：%v", err)
	}
	if result.Deployment.Status != "success" || state.id != "domain-1" || state.status != "valid" || state.updateCalled != 1 {
		t.Fatalf("成功部署后域名状态未恢复：%+v %+v", result.Deployment, state)
	}
	if state.expiresAt == nil || !state.expiresAt.Equal(notAfter) {
		t.Fatalf("成功部署后证书到期时间未更新：%v", state.expiresAt)
	}
}

func TestCertificateWorkflowDeployCurrentReturnsStateUpdateError(t *testing.T) {
	state := &workflowDomainState{
		status:    "error",
		updateErr: errors.New("数据库不可写"),
		item:      domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle},
	}
	workflow := &CertificateWorkflow{
		CurrentCertificate: func(context.Context, string) (certificate.IssueResult, error) {
			return certificate.IssueResult{DomainID: "domain-1"}, nil
		},
		Domains:    state,
		Servers:    workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment: &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
	}

	result, err := workflow.DeployCurrent(context.Background(), "domain-1")
	if err == nil || !strings.Contains(err.Error(), "更新域名证书状态失败") {
		t.Fatalf("域名状态更新失败时应返回明确错误：%v", err)
	}
	if result.Deployment.Status != "success" || state.updateCalled != 1 || state.status != "error" {
		t.Fatalf("状态更新失败时应保留部署报告和原状态，且不能静默忽略：%+v %+v", result.Deployment, state)
	}
}
