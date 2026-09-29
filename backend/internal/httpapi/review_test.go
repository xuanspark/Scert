package httpapi

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type stubDomainRepository struct {
	items     []domain.Domain
	created   domain.Domain
	loaded    domain.Domain
	updated   domain.Domain
	listErr   error
	createErr error
	getErr    error
	updateErr error
	deleteErr error
	stateErr  error
}

func (r *stubDomainRepository) UpdateCertificateState(_ context.Context, id, status string, expiresAt *time.Time) error {
	return r.stateErr
}

func (r *stubDomainRepository) Create(_ context.Context, item domain.Domain) error {
	r.created = item
	return r.createErr
}

func (r *stubDomainRepository) GetByID(_ context.Context, _ string) (domain.Domain, error) {
	if r.getErr != nil {
		return domain.Domain{}, r.getErr
	}
	if r.loaded.ID == "" {
		return domain.Domain{}, sql.ErrNoRows
	}
	return r.loaded, nil
}

func (r *stubDomainRepository) List(_ context.Context) ([]domain.Domain, error) {
	return r.items, r.listErr
}

func (r *stubDomainRepository) Delete(_ context.Context, _ string) error {
	return r.deleteErr
}

func (r *stubDomainRepository) Update(_ context.Context, item domain.Domain) error {
	r.updated = item
	return r.updateErr
}

func TestStatusReturnsApplicationAndConfigVersions(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("状态接口响应错误：%d", response.Code)
	}
	var payload struct {
		Version       string `json:"version"`
		ConfigVersion int    `json:"config_version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析状态响应失败：%v", err)
	}
	if payload.Version != buildinfo.Version || payload.ConfigVersion != config.CurrentVersion {
		t.Fatalf("状态版本字段不正确：%+v", payload)
	}
}

func TestPublicStatusDoesNotExposeInternalConfiguration(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Database.Driver = "mysql"
	cfg.Server.HTTPSMode = "proxy"
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("公开状态接口响应错误：%d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析公开状态响应失败：%v", err)
	}
	for _, key := range []string{"database_driver", "https_mode", "reset_url"} {
		if _, exists := payload[key]; exists {
			t.Fatalf("公开状态接口不应返回内部字段 %s：%s", key, response.Body.String())
		}
	}
}

func TestHTTPServerUsesProductionTimeoutsAndTLSMinimum(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second || server.ReadTimeout != 30*time.Second || server.WriteTimeout != 30*time.Second || server.IdleTimeout != 60*time.Second {
		t.Fatalf("HTTP 服务超时配置不正确：%+v", server)
	}
	if server.TLSConfig == nil || server.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS 最低版本应为 TLS 1.2：%+v", server.TLSConfig)
	}
}

func TestDomainAPIValidatesSANAndHidesInternalErrors(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	repo := &stubDomainRepository{}
	server := New(Dependencies{Config: cfg, Repository: repo, ProjectResources: projectResourceReaderStub{domains: repo}, ProjectDomains: projectDomainRepositoryStub{DomainRepository: repo}})

	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["bad_domain"]}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	serveAdminHTTP(server, invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || strings.Contains(invalidResponse.Body.String(), "detail") {
		t.Fatalf("非法 SAN 响应不正确：%d %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	valid := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["www.example.com"]}`))
	valid.Header.Set("Content-Type", "application/json")
	validResponse := httptest.NewRecorder()
	serveAdminHTTP(server, validResponse, valid)
	if validResponse.Code != http.StatusCreated || len(repo.created.SANs) != 2 || repo.created.SANs[0] != "example.com" {
		t.Fatalf("合法 SAN 响应不正确：%d %s", validResponse.Code, validResponse.Body.String())
	}

	repo.listErr = errors.New("internal-dsn-secret")
	listResponse := httptest.NewRecorder()
	serveAdminHTTP(server, listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil))
	if listResponse.Code != http.StatusInternalServerError {
		t.Fatalf("列表错误状态码不正确：%d", listResponse.Code)
	}
	if strings.Contains(listResponse.Body.String(), "internal-dsn-secret") || strings.Contains(listResponse.Body.String(), "detail") {
		t.Fatalf("接口泄露了底层错误：%s", listResponse.Body.String())
	}
}

func TestDeleteMissingDomainReturnsNotFound(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	repo := &stubDomainRepository{deleteErr: sql.ErrNoRows}
	server := New(Dependencies{
		Config: cfg, Repository: repo,
		ProjectResources: projectResourceReaderStub{domains: repo},
		ProjectDomains:   projectDomainRepositoryStub{DomainRepository: repo},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodDelete, "/api/v1/domains/missing", nil))
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "detail") {
		t.Fatalf("删除不存在域名响应不正确：%d %s", response.Code, response.Body.String())
	}
}

func TestUpdateDomainAPI(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	loaded := domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle, SANs: []string{"example.com"}, Status: "active"}
	repo := &stubDomainRepository{loaded: loaded}
	server := New(Dependencies{
		Config: cfg, Repository: repo,
		ProjectResources: projectResourceReaderStub{domains: repo},
		ProjectDomains:   projectDomainRepositoryStub{DomainRepository: repo},
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/domains/domain-1", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["www.example.com"],"auto_renew":false,"acme_environment":"production","dns_provider":"cloudflare"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("更新域名响应错误：%d %s", response.Code, response.Body.String())
	}
	if repo.updated.ID != loaded.ID || repo.updated.Status != "pending" || repo.updated.AutoRenew || repo.updated.ACMEEnvironment != domain.ACMEEnvironmentProduction || len(repo.updated.SANs) != 2 {
		t.Fatalf("更新域名参数不正确：%+v", repo.updated)
	}
}

func TestCreateDomainConflictReturnsConflict(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	repo := &stubDomainRepository{createErr: errors.New("UNIQUE constraint failed: domains.name")}
	server := New(Dependencies{Config: cfg, Repository: repo, ProjectDomains: projectDomainRepositoryStub{DomainRepository: repo}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"single"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("重复域名响应错误：%d %s", response.Code, response.Body.String())
	}
}

func TestUpdateDomainRejectsInvalidEnvironment(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	loaded := domain.Domain{ID: "domain-1", Name: "example.com"}
	repo := &stubDomainRepository{loaded: loaded}
	server := New(Dependencies{
		Config: cfg, Repository: repo,
		ProjectResources: projectResourceReaderStub{domains: repo},
		ProjectDomains:   projectDomainRepositoryStub{DomainRepository: repo},
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/domains/domain-1", strings.NewReader(`{"name":"example.com","certificate_type":"single","acme_environment":"invalid"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法 ACME 环境响应错误：%d %s", response.Code, response.Body.String())
	}
}

func TestIssueDomainUsesCertificateWorkflow(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	calledID := ""
	server := New(Dependencies{
		Config: cfg, Repository: &stubDomainRepository{},
		IssueCertificate: func(_ context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
			calledID = id
			return certificate.IssueResult{DomainID: id}, deployment.Report{Status: "success"}, nil
		},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/issue", nil))
	if response.Code != http.StatusOK || calledID != "domain-1" || !strings.Contains(response.Body.String(), `"status":"success"`) {
		t.Fatalf("证书申请接口响应错误：%d %s", response.Code, response.Body.String())
	}
}
