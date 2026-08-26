package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type stubDomainRepository struct {
	items     []domain.Domain
	created   domain.Domain
	listErr   error
	createErr error
	deleteErr error
}

func (r *stubDomainRepository) Create(_ context.Context, item domain.Domain) error {
	r.created = item
	return r.createErr
}

func (r *stubDomainRepository) GetByID(_ context.Context, _ string) (domain.Domain, error) {
	return domain.Domain{}, sql.ErrNoRows
}

func (r *stubDomainRepository) List(_ context.Context) ([]domain.Domain, error) {
	return r.items, r.listErr
}

func (r *stubDomainRepository) Delete(_ context.Context, _ string) error {
	return r.deleteErr
}

func TestStatusReturnsApplicationAndConfigVersions(t *testing.T) {
	cfg := config.Default(t.TempDir())
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
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

func TestDomainAPIValidatesSANAndHidesInternalErrors(t *testing.T) {
	cfg := config.Default(t.TempDir())
	repo := &stubDomainRepository{}
	server := New(Dependencies{Config: cfg, Repository: repo})

	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["bad_domain"]}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || strings.Contains(invalidResponse.Body.String(), "detail") {
		t.Fatalf("非法 SAN 响应不正确：%d %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	valid := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"san","sans":["www.example.com"]}`))
	valid.Header.Set("Content-Type", "application/json")
	validResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusCreated || len(repo.created.SANs) != 2 || repo.created.SANs[0] != "example.com" {
		t.Fatalf("合法 SAN 响应不正确：%d %s", validResponse.Code, validResponse.Body.String())
	}

	repo.listErr = errors.New("internal-dsn-secret")
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil))
	if listResponse.Code != http.StatusInternalServerError {
		t.Fatalf("列表错误状态码不正确：%d", listResponse.Code)
	}
	if strings.Contains(listResponse.Body.String(), "internal-dsn-secret") || strings.Contains(listResponse.Body.String(), "detail") {
		t.Fatalf("接口泄露了底层错误：%s", listResponse.Body.String())
	}
}

func TestDeleteMissingDomainReturnsNotFound(t *testing.T) {
	cfg := config.Default(t.TempDir())
	repo := &stubDomainRepository{deleteErr: sql.ErrNoRows}
	server := New(Dependencies{Config: cfg, Repository: repo})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/domains/missing", nil))
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "detail") {
		t.Fatalf("删除不存在域名响应不正确：%d %s", response.Code, response.Body.String())
	}
}
