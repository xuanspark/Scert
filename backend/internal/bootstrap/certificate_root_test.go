package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestCompleterPersistsAndUsesCustomCertificateRoot(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	request := fixture.request("admin.example.test")
	request.CertificateRoot = filepath.Join(t.TempDir(), "custom-certificates")
	result, err := fixture.completer(nil).Complete(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("completion failed: %v", err)
	}
	stored, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Paths.Certificate != request.CertificateRoot || !pathWithinCertificateRoot(request.CertificateRoot, stored.Server.TLSCert) {
		t.Fatalf("custom directory not used: root=%s certificate=%s", stored.Paths.Certificate, stored.Server.TLSCert)
	}
	if _, err := os.Stat(stored.Server.TLSCert); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateRootDefaultsAndRemovesOnlyItsProbe(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	root := fixture.base.Paths.Certificate
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "existing-file")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := prepareCertificateRoot(fixture.base.Paths, "")
	if err != nil || resolved != root {
		t.Fatalf("default directory failed: %s %v", resolved, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "existing-file" {
		t.Fatalf("probe leaked or existing data changed: %v %v", entries, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatal("existing data changed")
	}
}

func TestCertificateRootRejectsUnsafePaths(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	paths := fixture.base.Paths
	root := filepath.VolumeName(paths.Install) + string(filepath.Separator)
	for _, path := range []string{
		"relative/certificates", root, paths.Install, filepath.Dir(paths.Install),
		paths.Config, paths.Data, paths.Log, paths.Backup,
		filepath.Join(paths.Config, "certificates"), filepath.Join(paths.Data, "certificates"),
		paths.Certificate + "\ninvalid", paths.Certificate + "\x00invalid",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := prepareCertificateRoot(paths, path)
			var failure *CompleteError
			if !errors.As(err, &failure) || failure.Kind != CompleteErrorCertificatePath {
				t.Fatalf("unsafe path accepted: %v", err)
			}
		})
	}
}

func TestCertificateRootRejectsFilesAndSymlinks(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareCertificateRoot(fixture.base.Paths, file); err == nil {
		t.Fatal("file accepted as certificate directory")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	for _, path := range []string{link, filepath.Join(link, "nested")} {
		if _, err := prepareCertificateRoot(fixture.base.Paths, path); err == nil {
			t.Fatal("symbolic link accepted")
		}
	}
}

func TestCertificateRootRejectsUnwritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix file permissions without root privileges")
	}
	fixture := newCompletionFixture(t, "admin.example.test")
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	_, err := prepareCertificateRoot(fixture.base.Paths, root)
	var failure *CompleteError
	if !errors.As(err, &failure) || failure.Kind != CompleteErrorCertificateAccess {
		t.Fatalf("unwritable directory accepted: %v", err)
	}
}

func TestCompleterRejectsCertificateRootBeforeIssuing(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	request := fixture.request("admin.example.test")
	request.CertificateRoot = fixture.base.Paths.Config
	_, err := fixture.completer(nil).Complete(context.Background(), request)
	if err == nil || fixture.issuer.calls != 0 {
		t.Fatalf("unsafe directory reached certificate issuer: %v", err)
	}
	if _, err := os.Stat(fixture.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe directory committed configuration")
	}
}

func TestCertificateRootErrorsDoNotExposeUnderlyingDetails(t *testing.T) {
	for _, item := range []struct {
		kind   CompleteErrorKind
		status int
	}{
		{CompleteErrorCertificatePath, http.StatusBadRequest},
		{CompleteErrorCertificateAccess, http.StatusUnprocessableEntity},
	} {
		response := httptest.NewRecorder()
		writeBootstrapCompletionError(response, completeError(item.kind, "secret-value", errors.New("secret-value")))
		if response.Code != item.status || strings.Contains(response.Body.String(), "secret-value") {
			t.Fatalf("unsafe response: %d %s", response.Code, response.Body.String())
		}
	}
}
