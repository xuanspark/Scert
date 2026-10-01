package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestRecoveryHTTPSWaitsForActualCertificateSwitch(t *testing.T) {
	t.Parallel()
	previous, changed := workflowSavedCertificate(t), workflowSavedCertificate(t)
	roots := x509.NewCertPool()
	start := func(saved certificate.SavedResource) *httptest.Server {
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
	oldServer, newServer := start(previous), start(changed)
	validator := certificate.RemoteValidator{Roots: roots}
	for _, committed := range []bool{false, true} {
		name := "rollback"
		expected, finalURL, initialURL := previous.FingerprintSHA256, oldServer.URL, newServer.URL
		if committed {
			name, expected, finalURL, initialURL = "commit", changed.FingerprintSHA256, newServer.URL, oldServer.URL
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			baseline := deployment.RecoveryBaseline{Endpoint: "https://example.com", Domains: []string{"example.com"}, PreviousFingerprint: previous.FingerprintSHA256, ExpectedFingerprint: changed.FingerprintSHA256}
			result, err := verifyRecoveryHTTPS(t.Context(), func(ctx context.Context, endpoint, address string, domains []string) (certificate.ValidationResult, error) {
				calls++
				if endpoint != baseline.Endpoint || address != "127.0.0.1" || len(domains) != 1 || domains[0] != "example.com" {
					t.Fatal("恢复核验必须保留原目标、地址和域名校验")
				}
				url := initialURL
				if calls > 1 {
					url = finalURL
				}
				return validator.ValidateHTTPS(ctx, url, domains)
			}, deployment.Server{Address: "127.0.0.1"}, baseline, expected)
			if err != nil || calls != 2 || !strings.EqualFold(result.FingerprintSHA256, expected) {
				t.Fatalf("必须等待实际证书切换后才确认恢复：调用=%d，结果=%+v，错误=%v", calls, result, err)
			}
		})
	}
}

func TestRecoveryHTTPSFailsClosed(t *testing.T) {
	for _, name := range []string{"fingerprint_mismatch", "empty_fingerprint", "verification_error", "cancelled_match", "deadline"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer deadlineCancel()
			}
			expected := strings.Repeat("A", 64)
			sentinel := errors.New("证书信任或域名校验失败")
			calls := 0
			_, err := verifyRecoveryHTTPS(ctx, func(context.Context, string, string, []string) (certificate.ValidationResult, error) {
				calls++
				switch name {
				case "verification_error":
					return certificate.ValidationResult{}, sentinel
				case "empty_fingerprint":
					return certificate.ValidationResult{}, nil
				case "cancelled_match":
					cancel()
					return certificate.ValidationResult{FingerprintSHA256: expected}, nil
				default:
					return certificate.ValidationResult{FingerprintSHA256: strings.Repeat("B", 64)}, nil
				}
			}, deployment.Server{Address: "127.0.0.1"}, deployment.RecoveryBaseline{Endpoint: "https://example.com", Domains: []string{"example.com"}}, expected)
			if err == nil {
				t.Fatal("证据不足或核验中断不能认定恢复成功")
			}
			switch name {
			case "cancelled_match":
				if calls != 1 || !errors.Is(err, context.Canceled) {
					t.Fatalf("取消后不得接受迟到的匹配结果：调用=%d，错误=%v", calls, err)
				}
			case "deadline":
				if calls != 1 || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("恢复等待必须受截止时间约束：调用=%d，错误=%v", calls, err)
				}
			default:
				if calls != defaultRemoteVerificationAttempts {
					t.Fatalf("恢复核验次数必须有限：调用=%d，错误=%v", calls, err)
				}
				if name == "verification_error" && !errors.Is(err, sentinel) {
					t.Fatalf("不能忽略证书校验错误：%v", err)
				}
			}
		})
	}
}
