package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestHTTPChallengeProductionHandler(t *testing.T) {
	store := httpchallenge.NewStore()
	lease, err := store.NewLease(t.Context(), []string{"issued.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	const token = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	if err := lease.Present(t.Context(), "issued.example.com", token, token+"."+token); err != nil {
		t.Fatal(err)
	}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), HTTPChallenges: store})
	for _, tc := range []struct {
		host, path string
		status     int
	}{
		{"issued.example.com", token, 200},
		{"other.example.com", token, 404},
		{"issued.example.com", "unknown", 404},
		{"issued.example.com", token + "/", 404},
	} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://"+tc.host+httpchallenge.PathPrefix+tc.path, nil))
		if w.Code != tc.status || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("正式挑战路由错误：%d %s", w.Code, w.Body.String())
		}
	}
	server.httpChallenges = nil
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://issued.example.com"+httpchallenge.PathPrefix+token, nil))
	if w.Code != 404 {
		t.Fatal("缺失挑战存储时未拒绝")
	}
}

func TestHTTPDomainCreateAndUpdateBypassDNSOnly(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.DNS.Provider = "aliyun"
	repo := &stubDomainRepository{loaded: domain.Domain{ID: "domain-1", Name: "example.com", ChallengeType: domain.ChallengeHTTP01}}
	server := New(Dependencies{
		Config: cfg, Repository: repo,
		ProjectDomains:   projectDomainRepositoryStub{DomainRepository: repo},
		ProjectResources: projectResourceReaderStub{domains: repo},
	})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/v1/domains", `{"name":"example.com","certificate_type":"single","challenge_type":"http-01"}`, 201},
		{"POST", "/api/v1/domains", `{"name":"example.com","certificate_type":"san","sans":["*.example.com"],"challenge_type":"http-01"}`, 400},
		{"POST", "/api/v1/domains", `{"name":"example.com","certificate_type":"single","challenge_type":"unknown"}`, 400},
		{"PUT", "/api/v1/domains/domain-1", `{"name":"example.com","certificate_type":"single"}`, 200},
	} {
		w := httptest.NewRecorder()
		serveAdminHTTP(server, w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatalf("域名接口状态 %d，预期 %d：%s", w.Code, tc.status, w.Body.String())
		}
	}
	if repo.created.ChallengeType != domain.ChallengeHTTP01 || repo.updated.ChallengeType != domain.ChallengeHTTP01 {
		t.Fatal("HTTP 验证方式未保存或旧客户端覆盖了现有方式")
	}
}

func TestHTTPTaskAuthorizationRetainsProjectPermissions(t *testing.T) {
	for _, denied := range []domain.ProjectPermission{domain.PermissionDNSCredentialUse, domain.PermissionCertificateManage, domain.PermissionDeploymentExecute} {
		t.Run(string(denied), func(t *testing.T) {
			repo := &stubDomainRepository{loaded: domain.Domain{ID: "domain-1", Name: "example.com", ChallengeType: domain.ChallengeHTTP01, ResourceScope: domain.ResourceScope{ProjectID: "project-a"}}}
			calls := 0
			server := New(Dependencies{
				Config: readyTestConfig(t.TempDir()), Repository: repo,
				ProjectResources: projectResourceReaderStub{domains: repo},
				ProjectMembers: permissionProjectMemberRepository{allow: func(permission domain.ProjectPermission) error {
					if permission == denied {
						return repository.ErrProjectAccessDenied
					}
					return nil
				}},
				IssueCertificate: func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
					calls++
					return certificate.IssueResult{}, deployment.Report{}, nil
				},
			})
			w := httptest.NewRecorder()
			serveAdminHTTP(server, w, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
			want := denied == domain.PermissionDNSCredentialUse
			if (calls == 1) != want {
				t.Fatalf("HTTP 签发项目授权错误：%d %s", w.Code, w.Body.String())
			}
			err := server.authorizeCertificateTaskExecution(t.Context(), repository.Task{DomainID: "domain-1", HistoryScope: repository.HistoryScope{ProjectID: "project-a", ScopeType: repository.ScopeProject}, OperatorID: "actor", TaskType: "certificate_issue"}, repository.TaskAttempt{})
			if (err == nil) != want {
				t.Fatal("执行前授权与验证方式不匹配：", err)
			}
		})
	}
}
