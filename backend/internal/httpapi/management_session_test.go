package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestManagementSessionPinsCandidateAndExpires(t *testing.T) {
	f := newRotationFixture(t)
	certPath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(pair.Certificate[0]))
	var escaped *ManagementCertificateSession
	err = f.server.WithManagementCertificate(t.Context(), func(session *ManagementCertificateSession) error {
		escaped = session
		previous, err := session.Snapshot()
		if err != nil {
			return err
		}
		if err := f.server.WithManagementCertificate(t.Context(), func(*ManagementCertificateSession) error {
			t.Fatal("独占期间不能开始第二次协调")
			return nil
		}); err == nil {
			t.Fatal("重入协调未被拒绝")
		}
		saved := certificate.SavedResource{FullChainPath: certPath, PrivateKeyPath: keyPath}
		if err := session.Activate(previous, saved, previous.LeafHash); err == nil {
			t.Fatal("错误的候选摘要不能切换证书")
		}
		if err := session.Activate(previous, saved, hash); err != nil {
			return err
		}
		current, err := session.Snapshot()
		if err != nil {
			return err
		}
		if current.LeafHash != hash || current.State.CertificatePath != certPath {
			t.Fatal("切换后快照没有反映候选")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.Snapshot(); err == nil {
		t.Fatal("回调结束后不能读取独占快照")
	}
	if err := escaped.Activate(ManagementSnapshot{}, certificate.SavedResource{}, hash); err == nil {
		t.Fatal("回调结束后不能切换证书")
	}
	if serial, err := f.serial(); err != nil || serial != 3 {
		t.Fatal("真实 TLS 未切换", serial, err)
	}
}

func TestManagementSnapshotRejectsDivergence(t *testing.T) {
	for _, layer := range []string{"config", "material", "unstarted"} {
		t.Run(layer, func(t *testing.T) {
			f := newRotationFixture(t)
			certPath, keyPath := f.issue(t, 3, "management.example.test", time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
			switch layer {
			case "config":
				cfg, err := config.Load(f.server.configPath)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Server.TLSCert, cfg.Server.TLSKey = certPath, keyPath
				if err := cfg.Save(f.server.configPath); err != nil {
					t.Fatal(err)
				}
			case "material":
				for destination, source := range map[string]string{f.firstCertificate: certPath, f.firstKey: keyPath} {
					data, err := os.ReadFile(source)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(destination, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "unstarted":
				f.server.tlsCertificate.Store(nil)
			}
			if err := f.server.WithManagementCertificate(t.Context(), func(session *ManagementCertificateSession) error {
				_, err := session.Snapshot()
				return err
			}); err == nil {
				t.Fatal("配置、材料与运行快照不一致时不能报告可协调")
			}
		})
	}
}

func TestManagementSessionDoesNotBlockTLSOrReadiness(t *testing.T) {
	f := newRotationFixture(t)
	f.server.secrets = readinessStore(t)
	f.server.configMu.Lock()
	f.server.config.DNS.Provider = "aliyun"
	f.server.config.DNS.CredentialRef = readinessDNSReference
	f.server.configMu.Unlock()
	if err := f.server.WithManagementCertificate(t.Context(), func(session *ManagementCertificateSession) error {
		if _, err := session.Snapshot(); err != nil {
			return err
		}
		if serial, err := f.serial(); err != nil || serial != 2 {
			return fmt.Errorf("独占期间握手失败：%d %v", serial, err)
		}
		request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		request.Host = "management.example.test"
		response := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			return fmt.Errorf("独占期间就绪探测失败：%d %s", response.Code, response.Body.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManagementSessionExcludesConfigurationWriters(t *testing.T) {
	f := newRotationFixture(t)
	f.server.configMu.Lock()
	f.server.config.Admin.PasswordHash = httpUserPasswordHash(t, "AdminPass123")
	f.server.configMu.Unlock()
	cookie := loginAuthTest(t, f.server, "admin", "AdminPass123", false).Result().Cookies()[0]
	if err := f.server.WithManagementCertificate(t.Context(), func(*ManagementCertificateSession) error {
		response := performChangePasswordRequest(t, f.server, cookie, `{"current_password":"AdminPass123","new_password":"NewAdmin123","confirm_password":"NewAdmin123"}`)
		if response.Code != http.StatusConflict {
			t.Fatalf("协调期间改密应拒绝并发保存：%d %s", response.Code, response.Body.String())
		}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		response = httptest.NewRecorder()
		f.server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("协调期间设置应拒绝并发保存：%d %s", response.Code, response.Body.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.server.WithManagementCertificate(ctx, func(*ManagementCertificateSession) error {
		t.Fatal("已取消操作不能进入独占区")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
