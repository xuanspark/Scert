package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type certificateReaderStub struct {
	result   certificate.IssueResult
	versions []certificate.SavedResource
	err      error
}

func (s certificateReaderStub) Current(context.Context, string) (certificate.IssueResult, error) {
	return s.result, s.err
}

func (s certificateReaderStub) ListVersions(context.Context, string) ([]certificate.SavedResource, error) {
	return s.versions, s.err
}

func TestCertificateReadAPIs(t *testing.T) {
	expiresAt := time.Now().UTC().Add(90 * 24 * time.Hour)
	reader := certificateReaderStub{
		result: certificate.IssueResult{
			DomainID: "domain-1",
			Domains:  []string{"example.com", "www.example.com"},
			Saved: certificate.SavedResource{
				Domain:          "example.com",
				CertificateType: "san",
				Version:         "20260827-120000.000000000",
				NotAfter:        expiresAt,
			},
		},
		versions: []certificate.SavedResource{{Version: "20260827-120000.000000000"}},
	}
	repo := &stubDomainRepository{items: []domain.Domain{{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSAN}}}
	server := New(Dependencies{Config: config.Default(t.TempDir()), Repository: repo, CurrentCertificate: reader})

	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/certificates", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "example.com") {
		t.Fatalf("证书列表响应错误：%d %s", list.Code, list.Body.String())
	}

	detail := httptest.NewRecorder()
	server.Handler().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/v1/certificates/domain-1", nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "20260827-120000.000000000") {
		t.Fatalf("证书详情响应错误：%d %s", detail.Code, detail.Body.String())
	}

	versions := httptest.NewRecorder()
	server.Handler().ServeHTTP(versions, httptest.NewRequest(http.MethodGet, "/api/v1/domains/domain-1/certificate/versions", nil))
	if versions.Code != http.StatusOK || !strings.Contains(versions.Body.String(), "20260827-120000.000000000") {
		t.Fatalf("证书历史版本响应错误：%d %s", versions.Code, versions.Body.String())
	}
}

func TestCertificateImportAPIAcceptsMultipartFiles(t *testing.T) {
	var received certificate.ImportRequest
	importer := func(_ context.Context, request certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
		received = request
		certificateData, err := os.ReadFile(request.Certificate)
		if err != nil {
			return certificate.IssueResult{}, deployment.Report{}, err
		}
		privateKeyData, err := os.ReadFile(request.PrivateKey)
		if err != nil {
			return certificate.IssueResult{}, deployment.Report{}, err
		}
		if string(certificateData) != "certificate" || string(privateKeyData) != "private-key" {
			return certificate.IssueResult{}, deployment.Report{}, os.ErrInvalid
		}
		return certificate.IssueResult{DomainID: request.DomainID}, deployment.Report{Status: "success"}, nil
	}
	server := New(Dependencies{
		Config:            config.Default(t.TempDir()),
		Repository:        &stubDomainRepository{},
		ImportCertificate: importer,
	})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	certificatePart, err := writer.CreateFormFile("certificate", "cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = certificatePart.Write([]byte("certificate"))
	privateKeyPart, err := writer.CreateFormFile("private_key", "key.pem")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = privateKeyPart.Write([]byte("private-key"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || received.DomainID != "domain-1" {
		t.Fatalf("证书导入响应错误：%d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(received.Certificate); !os.IsNotExist(err) {
		t.Fatalf("证书上传临时文件未在请求结束后清理：%s", received.Certificate)
	}
}

func TestCertificateImportAPIRejectsMissingFiles(t *testing.T) {
	server := New(Dependencies{
		Config:     config.Default(t.TempDir()),
		Repository: &stubDomainRepository{},
		ImportCertificate: func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{}, nil
		},
	})
	body, _ := json.Marshal(certificatePathRequest{CertificatePath: "", PrivateKeyPath: ""})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("缺少证书路径应返回参数错误：%d %s", response.Code, response.Body.String())
	}
}
