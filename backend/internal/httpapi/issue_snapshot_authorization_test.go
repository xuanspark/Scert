package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

var errSnapshotAccountReached = errors.New("测试已通过授权并到达账户阶段")

type snapshotTestAccounts struct{}

func (snapshotTestAccounts) LoadOrCreate(context.Context, string, string) (*certificate.AccountUser, error) {
	return nil, errSnapshotAccountReached
}

func (snapshotTestAccounts) Save(context.Context, string, *certificate.AccountUser) error {
	return errors.New("本测试不应保存账户")
}

// 在前置授权和实际签发读取之间确定性切换快照，不依赖不稳定的调度时序。
func TestHTTPIssueReauthorizesActualSnapshot(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, change := range []string{"unchanged", "dns", "legacy_dns", "project", "domain", "certificate_revoked", "deployment_revoked"} {
			t.Run(map[bool]string{false: "sync", true: "async"}[async]+"/"+change, func(t *testing.T) {
				initial := domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle, ChallengeType: domain.ChallengeHTTP01, ResourceScope: domain.ResourceScope{ProjectID: "default"}}
				visible := &stubDomainRepository{loaded: initial}
				actual := &stubDomainRepository{loaded: initial}
				var server *Server
				var issueErr error
				started := false
				operation := func(ctx context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
					started = true
					switch change {
					case "dns":
						actual.loaded.ChallengeType = domain.ChallengeDNS01
					case "legacy_dns":
						actual.loaded.ChallengeType = ""
					case "project":
						actual.loaded.ProjectID = "another-project"
					case "domain":
						actual.loaded.ID = "another-domain"
					}
					service := certificate.NewService(server.config, actual, nil, nil, snapshotTestAccounts{})
					service.SetHTTPChallenges(httpchallenge.NewStore())
					result, err := service.Issue(ctx, id)
					issueErr = err
					return result, deployment.Report{}, err
				}
				if async {
					var cleanup func()
					server, _, _, cleanup = newTaskAPITestServer(t, operation)
					defer cleanup()
				} else {
					server = New(Dependencies{Config: readyTestConfig(t.TempDir()), Repository: visible, IssueCertificate: operation})
				}
				server.projectResources = projectResourceReaderStub{domains: visible}
				server.projectMembers = permissionProjectMemberRepository{allow: func(permission domain.ProjectPermission) error {
					if permission == domain.PermissionDNSCredentialUse || started && (change == "certificate_revoked" && permission == domain.PermissionCertificateManage ||
						change == "deployment_revoked" && permission == domain.PermissionDeploymentExecute) {
						return repository.ErrProjectAccessDenied
					}
					return nil
				}}
				response := httptest.NewRecorder()
				serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
				if async {
					submittedTask(t, response)
					waitTaskWorkersForTest(t, server)
				}
				want := repository.ErrProjectAccessDenied
				if change == "unchanged" {
					want = errSnapshotAccountReached
				}
				if !started || !errors.Is(issueErr, want) {
					t.Fatalf("实际签发配置未正确重新授权：started=%v err=%v，响应=%d %s", started, issueErr, response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestHTTPIssueSnapshotRetainsRetryPermission(t *testing.T) {
	item := domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle, ChallengeType: domain.ChallengeHTTP01, ResourceScope: domain.ResourceScope{ProjectID: "default"}}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), ProjectMembers: permissionProjectMemberRepository{allow: func(permission domain.ProjectPermission) error {
		if permission == domain.PermissionTaskRetry {
			return repository.ErrProjectAccessDenied
		}
		return nil
	}}})
	service := certificate.NewService(server.config, &stubDomainRepository{loaded: item}, nil, nil, snapshotTestAccounts{})
	service.SetHTTPChallenges(httpchallenge.NewStore())
	ctx := server.withCertificateIssueAuthorization(t.Context(), "actor", item.ID, "default", true)
	if _, err := service.Issue(ctx, item.ID); !errors.Is(err, repository.ErrProjectAccessDenied) {
		t.Fatal("重试授权未在实际签发快照处生效：", err)
	}
}
