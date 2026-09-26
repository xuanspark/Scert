package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
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
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), Repository: repo, CurrentCertificate: reader})

	list := httptest.NewRecorder()
	serveAdminHTTP(server, list, httptest.NewRequest(http.MethodGet, "/api/v1/certificates", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "example.com") {
		t.Fatalf("证书列表响应错误：%d %s", list.Code, list.Body.String())
	}

	detail := httptest.NewRecorder()
	serveAdminHTTP(server, detail, httptest.NewRequest(http.MethodGet, "/api/v1/certificates/domain-1", nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "20260827-120000.000000000") {
		t.Fatalf("证书详情响应错误：%d %s", detail.Code, detail.Body.String())
	}

	versions := httptest.NewRecorder()
	serveAdminHTTP(server, versions, httptest.NewRequest(http.MethodGet, "/api/v1/domains/domain-1/certificate/versions", nil))
	if versions.Code != http.StatusOK || !strings.Contains(versions.Body.String(), "20260827-120000.000000000") {
		t.Fatalf("证书历史版本响应错误：%d %s", versions.Code, versions.Body.String())
	}
}

func TestCertificateListHidesReaderErrors(t *testing.T) {
	reader := certificateReaderStub{err: errors.New("private-key-secret-path")}
	repo := &stubDomainRepository{
		items: []domain.Domain{{ID: "domain-1", Name: "example.com"}},
	}
	server := New(Dependencies{
		Config:             readyTestConfig(t.TempDir()),
		Repository:         repo,
		CurrentCertificate: reader,
	})

	response := httptest.NewRecorder()
	serveAdminHTTP(
		server,
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/certificates", nil),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("certificate list request failed: %d %s", response.Code, response.Body.String())
	}

	body := response.Body.String()
	if strings.Contains(body, "private-key-secret-path") {
		t.Fatalf("certificate list leaked an internal error: %s", body)
	}
	if !strings.Contains(body, "\u5f53\u524d\u8bc1\u4e66\u4e0d\u53ef\u7528") {
		t.Fatalf("certificate list did not include the safe error message: %s", body)
	}
}
func TestCertificateDeployAPIHidesWorkflowErrors(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
		DeployCertificate: func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{}, errors.New("private-key-secret-path")
		},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/deploy", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("certificate deployment failure returned an unexpected status: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "private-key-secret-path") {
		t.Fatalf("certificate deployment leaked an internal error: %s", body)
	}
	if !strings.Contains(body, "\u8bc1\u4e66\u90e8\u7f72\u5931\u8d25") {
		t.Fatalf("certificate deployment did not include the safe error message: %s", body)
	}
}

func TestCertificateImportAPIHidesWorkflowErrors(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
		ImportCertificate: func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{}, errors.New("private-key-secret-path")
		},
	})
	body := `{"certificate_path":"/ssl/cert.pem","private_key_path":"/ssl/key.pem"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("certificate import failure returned an unexpected status: %d %s", response.Code, response.Body.String())
	}
	responseBody := response.Body.String()
	if strings.Contains(responseBody, "private-key-secret-path") {
		t.Fatalf("certificate import leaked an internal error: %s", responseBody)
	}
	if !strings.Contains(responseBody, "\u8bc1\u4e66\u5bfc\u5165\u5931\u8d25") {
		t.Fatalf("certificate import did not include the safe error message: %s", responseBody)
	}
}
func TestCertificateImportAPIReportsSavedCertificateOnDeploymentFailure(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved=%t", saved), func(t *testing.T) {
			server := New(Dependencies{
				Config: readyTestConfig(t.TempDir()), Repository: &stubDomainRepository{},
				ImportCertificate: func(_ context.Context, request certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
					result := certificate.IssueResult{}
					if saved {
						result = certificate.IssueResult{DomainID: request.DomainID, Saved: certificate.SavedResource{
							CertificatePath: "/ssl/cert.pem", FullChainPath: "/ssl/fullchain.pem", PrivateKeyPath: "/ssl/key.pem",
						}}
					}
					return result, deployment.Report{Total: 1, Failed: 1, Status: "failed", Results: []deployment.Result{
						{ServerID: "server-1", Server: "验收服务器", Status: "failed", Message: "secret-diagnostic"},
					}}, errors.New("secret-diagnostic")
				},
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", strings.NewReader(`{"certificate_path":"/ssl/cert.pem","private_key_path":"/ssl/key.pem"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, request)
			var result struct {
				Imported   bool              `json:"imported"`
				Error      string            `json:"error"`
				Deployment deployment.Report `json:"deployment"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadGateway || result.Imported != saved || len(result.Deployment.Results) != 1 {
				t.Fatalf("导入和部署阶段未正确区分：%d %s", response.Code, response.Body.String())
			}
			if saved && (!strings.Contains(result.Error, "证书已导入") || !strings.Contains(result.Error, "无需重新导入")) {
				t.Fatalf("缺少部署重试提示：%s", result.Error)
			}
			if !saved && result.Error != "证书导入失败" {
				t.Fatalf("未保存证书却报告导入成功：%s", result.Error)
			}
			if strings.Contains(response.Body.String(), "secret-diagnostic") {
				t.Fatal("部署诊断泄露到响应")
			}
		})
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
		Config:            readyTestConfig(t.TempDir()),
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
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusOK || received.DomainID != "domain-1" {
		t.Fatalf("证书导入响应错误：%d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(received.Certificate); !os.IsNotExist(err) {
		t.Fatalf("证书上传临时文件未在请求结束后清理：%s", received.Certificate)
	}
}

func TestCertificateImportAPIRejectsMissingFiles(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
		ImportCertificate: func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{}, nil
		},
	})
	body, _ := json.Marshal(certificatePathRequest{CertificatePath: "", PrivateKeyPath: ""})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("缺少证书路径应返回参数错误：%d %s", response.Code, response.Body.String())
	}
}

func TestCertificateImportAPIRejectsInvalidPathJSON(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
		ImportCertificate: func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
			t.Fatal("无效证书路径请求不应调用导入服务")
			return certificate.IssueResult{}, deployment.Report{}, nil
		},
	})

	tests := []string{
		`{"certificate_path":"/ssl/cert.pem","private_key_path":"/ssl/key.pem","unexpected":true}`,
		`{"certificate_path":"/ssl/cert.pem","private_key_path":"/ssl/key.pem"}{"certificate_path":"again"}`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		serveAdminHTTP(server, response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("无效证书路径请求应返回 400，实际为 %d：%s", response.Code, response.Body.String())
		}
	}
}

func TestCertificateDeployAPISanitizesReportDetails(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
		DeployCertificate: func(context.Context, string) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{
				Total:  1,
				Failed: 1,
				Status: "failed",
				Results: []deployment.Result{{
					ServerID: "server-1",
					Server:   "生产服务器",
					Status:   "failed",
					Message:  "上传远程证书失败：/etc/nginx/private/key.pem: permission denied",
				}},
			}, errors.New("private-key-secret-path")
		},
	})
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/deploy", nil))
	body := response.Body.String()
	if !strings.Contains(body, "证书部署失败") {
		t.Fatalf("证书部署失败响应缺少稳定错误：%s", body)
	}
	if strings.Contains(body, "/etc/nginx/private/key.pem") || strings.Contains(body, "permission denied") {
		t.Fatalf("证书部署响应泄露了报告明细：%s", body)
	}
	if !strings.Contains(body, "生产服务器") || !strings.Contains(body, "server-1") {
		t.Fatalf("证书部署响应缺少安全的服务器信息：%s", body)
	}
}

func TestCertificateImportAPISanitizesReportDetails(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
		ImportCertificate: func(context.Context, certificate.ImportRequest) (certificate.IssueResult, deployment.Report, error) {
			return certificate.IssueResult{}, deployment.Report{
				Total:  1,
				Failed: 1,
				Status: "failed",
				Results: []deployment.Result{{
					ServerID: "server-1",
					Server:   "生产服务器",
					Status:   "failed",
					Message:  "上传远程证书失败：/etc/nginx/private/key.pem: permission denied",
				}},
			}, errors.New("private-key-secret-path")
		},
	})
	body := `{"certificate_path":"/ssl/cert.pem","private_key_path":"/ssl/key.pem"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains/domain-1/certificate/import", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	responseBody := response.Body.String()
	if !strings.Contains(responseBody, "证书导入失败") {
		t.Fatalf("证书导入失败响应缺少稳定错误：%s", responseBody)
	}
	if strings.Contains(responseBody, "/etc/nginx/private/key.pem") || strings.Contains(responseBody, "permission denied") {
		t.Fatalf("证书导入响应泄露了报告明细：%s", responseBody)
	}
	if !strings.Contains(responseBody, "生产服务器") || !strings.Contains(responseBody, "server-1") {
		t.Fatalf("证书导入响应缺少安全的服务器信息：%s", responseBody)
	}
}
