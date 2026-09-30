package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func workflowSavedCertificate(t *testing.T) certificate.SavedResource {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"example.com"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	saved := certificate.SavedResource{
		CertificatePath:   filepath.Join(directory, "cert.pem"),
		FullChainPath:     filepath.Join(directory, "fullchain.pem"),
		PrivateKeyPath:    filepath.Join(directory, "key.pem"),
		FingerprintSHA256: fmt.Sprintf("%X", sha256.Sum256(der)), NotAfter: template.NotAfter,
	}
	for path, data := range map[string][]byte{
		saved.CertificatePath: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		saved.FullChainPath:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		saved.PrivateKeyPath:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return saved
}

func TestDeploymentVerificationRejectsWrongOrMissingCertificateIdentity(t *testing.T) {
	for _, action := range []string{"issue", "deploy", "import"} {
		for _, remoteFingerprint := range []string{strings.Repeat("A", 64), ""} {
			t.Run(action+"/"+remoteFingerprint, func(t *testing.T) {
				saved := workflowSavedCertificate(t)
				issued := certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved}
				state := &workflowDomainState{}
				deployer := &workflowDeployer{report: deployment.Report{
					Total: 1, Success: 1, Status: "success",
					Results: []deployment.Result{{ServerID: "server-1", Status: "success", Message: "文件已推送并重载"}},
				}}
				workflow := &CertificateWorkflow{
					Certificates:       &workflowCertificateService{result: issued},
					CurrentCertificate: func(context.Context, string) (certificate.IssueResult, error) { return issued, nil },
					Domains:            state, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
					Deployment: deployer, RemoteVerificationAttempts: 1,
					RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
						return certificate.ValidationResult{FingerprintSHA256: remoteFingerprint}, nil
					},
				}
				var result CertificateWorkflowResult
				var err error
				switch action {
				case "issue":
					result, err = workflow.IssueWithReport(t.Context(), issued.DomainID)
				case "deploy":
					result, err = workflow.DeployCurrent(t.Context(), issued.DomainID)
				case "import":
					result, err = workflow.Import(t.Context(), certificate.ImportRequest{DomainID: issued.DomainID})
				}
				if err == nil || result.Deployment.Status != "failed" || result.Deployment.VerificationStatus != "failed" || state.status != "error" {
					t.Fatalf("错误或缺失证书身份不得报告生效成功：%+v，%s，%v", result.Deployment, state.status, err)
				}
				if result.Deployment.Success != 1 || result.Deployment.Results[0].Status != "success" {
					t.Fatalf("站点校验失败不得伪造单台文件推送失败或回滚：%+v", result.Deployment)
				}
			})
		}
	}
}

func TestDeploymentVerificationFailureDoesNotReturnSuccessfulReport(t *testing.T) {
	saved := workflowSavedCertificate(t)
	workflow := &CertificateWorkflow{
		Domains: &workflowDomainState{}, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment:                 &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
		RemoteVerificationAttempts: 1,
		RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
			return certificate.ValidationResult{}, errors.New("模拟站点不可达")
		},
	}
	report, err := workflow.deploy(t.Context(), certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved})
	if err == nil || report.Status != "failed" || report.VerificationStatus != "failed" {
		t.Fatalf("站点不可达时不得保留成功汇总：%+v，%v", report, err)
	}
}

func TestDeploymentVerificationUsesFileIdentityInsteadOfMetadata(t *testing.T) {
	for _, metadata := range []string{"", strings.Repeat("B", 64)} {
		t.Run(metadata, func(t *testing.T) {
			saved := workflowSavedCertificate(t)
			expected := saved.FingerprintSHA256
			saved.FingerprintSHA256 = metadata
			workflow := &CertificateWorkflow{
				Domains: &workflowDomainState{}, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
				Deployment:                 &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
				RemoteVerificationAttempts: 1,
				RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
					return certificate.ValidationResult{FingerprintSHA256: strings.ToLower(expected)}, nil
				},
			}
			report, err := workflow.deploy(t.Context(), certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved})
			if err != nil || report.Status != "success" || report.VerificationStatus != "success" {
				t.Fatalf("应按实际文件指纹确认生效，不依赖元数据或大小写：%+v，%v", report, err)
			}
		})
	}
}

func TestDeploymentVerificationRejectsInvalidLocalFilesBeforeRemoteWrite(t *testing.T) {
	for _, broken := range []string{"certificate", "chain", "key"} {
		t.Run(broken, func(t *testing.T) {
			saved := workflowSavedCertificate(t)
			path := map[string]string{"certificate": saved.CertificatePath, "chain": saved.FullChainPath, "key": saved.PrivateKeyPath}[broken]
			if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			deployer := &workflowDeployer{}
			workflow := &CertificateWorkflow{
				Domains: &workflowDomainState{}, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
				Deployment: deployer,
				RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
					t.Fatal("本地文件损坏时不应执行远端校验")
					return certificate.ValidationResult{}, nil
				},
			}
			report, err := workflow.deploy(t.Context(), certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved})
			if err == nil || report.Status != "failed" || deployer.targets != nil || report.VerificationStatus != "" {
				t.Fatalf("本地文件损坏后不得推送或声称已校验站点：%+v，%v", report, err)
			}
		})
	}
}

func TestDeploymentVerificationRetriesRealValidOldCertificate(t *testing.T) {
	oldSaved, newSaved := workflowSavedCertificate(t), workflowSavedCertificate(t)
	roots := x509.NewCertPool()
	startServer := func(saved certificate.SavedResource) *httptest.Server {
		pair, err := tls.LoadX509KeyPair(saved.CertificatePath, saved.PrivateKeyPath)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		roots.AddCert(leaf)
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		server.StartTLS()
		t.Cleanup(server.Close)
		return server
	}
	oldServer, newServer := startServer(oldSaved), startServer(newSaved)
	validator := certificate.RemoteValidator{Roots: roots}
	calls, waits := 0, 0
	workflow := &CertificateWorkflow{
		Domains: &workflowDomainState{}, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment:                 &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
		RemoteVerificationAttempts: 2,
		RemoteVerificationWait:     func(context.Context, time.Duration) error { waits++; return nil },
		RemoteVerifier: func(ctx context.Context, _ string, domains []string) (certificate.ValidationResult, error) {
			calls++
			endpoint := oldServer.URL
			if calls > 2 {
				endpoint = newServer.URL
			}
			return validator.ValidateHTTPS(ctx, endpoint, domains)
		},
	}
	report, err := workflow.deploy(t.Context(), certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: newSaved})
	if err != nil || report.VerificationStatus != "success" || calls != 3 || waits != 1 {
		t.Fatalf("有效旧证书必须重试，只有实际新证书才能通过：%+v，调用 %d，等待 %d，%v", report, calls, waits, err)
	}
}

func TestDeploymentVerificationCancellationCannotBecomeSuccess(t *testing.T) {
	saved := workflowSavedCertificate(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	workflow := &CertificateWorkflow{
		Domains: &workflowDomainState{}, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment: &workflowDeployer{report: deployment.Report{Total: 1, Success: 1, Status: "success"}},
		RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
			calls++
			if calls > 1 {
				cancel()
			}
			return certificate.ValidationResult{FingerprintSHA256: saved.FingerprintSHA256}, nil
		},
	}
	report, err := workflow.deploy(ctx, certificate.IssueResult{DomainID: "domain-1", Domains: []string{"example.com"}, Saved: saved})
	if !errors.Is(err, context.Canceled) || report.Status != "failed" || report.VerificationStatus != "failed" {
		t.Fatalf("取消后不得接受迟到的成功校验：%+v，%v", report, err)
	}
}

func TestDeploymentVerificationFailurePersistsFailedTask(t *testing.T) {
	tracked, app := newCertificateTaskFixture(t)
	saved := workflowSavedCertificate(t)
	tracked.workflow = &CertificateWorkflow{
		CurrentCertificate: func(context.Context, string) (certificate.IssueResult, error) {
			return certificate.IssueResult{DomainID: "task-domain", Domains: []string{"example.com"}, Saved: saved}, nil
		},
		Domains: &workflowDomainState{}, Servers: workflowServerLister{servers: []deployment.Server{{ID: "server-1"}}},
		Deployment: &workflowDeployer{report: deployment.Report{
			Total: 1, Success: 1, Status: "success", Results: []deployment.Result{{ServerID: "server-1", Status: "success"}},
		}},
		RemoteVerificationAttempts: 1,
		RemoteVerifier: func(context.Context, string, []string) (certificate.ValidationResult, error) {
			return certificate.ValidationResult{FingerprintSHA256: strings.Repeat("A", 64)}, nil
		},
	}
	result, err := tracked.DeployCurrent(t.Context(), "task-domain")
	if err == nil || result.Deployment.VerificationStatus != "failed" {
		t.Fatalf("任务工作流未保留校验失败：%+v，%v", result, err)
	}
	task := assertCertificateTask(t, app, "failed", "failed")
	items, err := app.Tasks.ListTaskDeploymentResults(t.Context(), task.ID)
	if err != nil || len(items) != 1 || items[0].Status != "success" {
		t.Fatalf("失败任务应保留真实的文件推送结果：%+v，%v", items, err)
	}
}
