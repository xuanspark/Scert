package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type restoreCertificateReader struct {
	versions []certificate.SavedResource
}

func (r restoreCertificateReader) Current(context.Context, string) (certificate.IssueResult, error) {
	return certificate.IssueResult{}, nil
}

func (r restoreCertificateReader) ListVersions(context.Context, string) ([]certificate.SavedResource, error) {
	return r.versions, nil
}

type restoreAuditRepository struct {
	items []repository.AuditLog
}

func (r *restoreAuditRepository) CreateAuditLog(_ context.Context, item repository.AuditLog) error {
	r.items = append(r.items, item)
	return nil
}

func newRestoreAPITestServer(t *testing.T, restore func(context.Context, string, string) (certificate.IssueResult, error)) (*Server, *restoreAuditRepository) {
	t.Helper()
	audit := &restoreAuditRepository{}
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "RestoreTest123")
	server := New(Dependencies{
		Config:                    cfg,
		Repository:                &stubDomainRepository{},
		CurrentCertificate:        restoreCertificateReader{},
		RestoreCertificateVersion: restore,
		Audit:                     audit,
	})
	server.authSessions["test-session"] = authSession{
		UserID:            "primary-super-admin",
		PasswordHash:      cfg.Admin.PasswordHash,
		Email:             cfg.Admin.Email,
		Username:          "admin",
		Role:              "super_admin",
		ExpiresAt:         time.Now().Add(time.Hour),
		AbsoluteExpiresAt: time.Now().Add(2 * time.Hour),
	}
	return server, audit
}

func TestRestoreCertificateVersionAPIRecordsSuccessAudit(t *testing.T) {
	called := false
	server, audit := newRestoreAPITestServer(t, func(_ context.Context, domainID, version string) (certificate.IssueResult, error) {
		called = true
		if domainID != "domain-1" || version != "20260830-120000" {
			t.Fatalf("unexpected restore request: %s %s", domainID, version)
		}
		return certificate.IssueResult{DomainID: domainID, Saved: certificate.SavedResource{Version: version}}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/versions/20260830-120000/restore", nil)
	request.AddCookie(&http.Cookie{Name: authCookieName, Value: "test-session"})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "20260830-120000") {
		t.Fatalf("unexpected success response: %d %s", response.Code, response.Body.String())
	}
	if !called {
		t.Fatal("restore callback was not called")
	}
	if len(audit.items) != 1 || audit.items[0].Status != "success" || audit.items[0].Action != "certificate_version_restore" {
		t.Fatalf("success audit was not recorded: %+v", audit.items)
	}
}

func TestRestoreCertificateVersionAPIRecordsFailureAudit(t *testing.T) {
	server, audit := newRestoreAPITestServer(t, func(context.Context, string, string) (certificate.IssueResult, error) {
		return certificate.IssueResult{}, errors.New("private-key-secret-path")
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/versions/bad/restore", nil)
	request.AddCookie(&http.Cookie{Name: authCookieName, Value: "test-session"})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected failure response: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-key-secret-path") {
		t.Fatalf("certificate restore leaked an internal error: %s", response.Body.String())
	}
	if strings.Contains(audit.items[0].Message, "private-key-secret-path") {
		t.Fatalf("certificate restore audit leaked an internal error: %+v", audit.items[0])
	}
	if len(audit.items) != 1 || audit.items[0].Status != "failed" || audit.items[0].Action != "certificate_version_restore" {
		t.Fatalf("failure audit was not recorded: %+v", audit.items)
	}
}

func TestListCertificateVersionsAPIRequiresAuthentication(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = true
	server := New(Dependencies{
		Config:             cfg,
		Repository:         &stubDomainRepository{},
		CurrentCertificate: restoreCertificateReader{versions: []certificate.SavedResource{{Domain: "example.com", Version: "v1"}}},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/domains/domain-1/certificate/versions", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated version request should be rejected: %d", response.Code)
	}
}

var _ repository.DomainRepository = (*stubDomainRepository)(nil)
var _ = domain.Domain{}
