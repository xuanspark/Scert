package releasearchive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCreateTarGzPreservesReleaseModes(t *testing.T) {
	source := createFixture(t)
	destination := filepath.Join(t.TempDir(), "Scert-test.tar.gz")
	if err := Create(Options{
		Source: source, Destination: destination, Format: FormatTarGz,
		Executables: []string{"install-linux.sh", "scert", "scert-admin", "scert-nginx", "ScertDesktop"},
	}); err != nil {
		t.Fatalf("创建 Linux 发布归档失败：%v", err)
	}

	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	modes := map[string]int64{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		modes[strings.TrimSuffix(header.Name, "/")] = header.Mode
	}
	assertMode(t, modes, "install-linux.sh", 0o755)
	assertMode(t, modes, "scert", 0o755)
	assertMode(t, modes, "scert-admin", 0o755)
	assertMode(t, modes, "scert-nginx", 0o755)
	assertMode(t, modes, "ScertDesktop", 0o755)
	assertMode(t, modes, "web/index.html", 0o644)
	assertMode(t, modes, "web", 0o755)
}

func TestCreateZipContainsReleaseFiles(t *testing.T) {
	source := createFixture(t)
	destination := filepath.Join(t.TempDir(), "Scert-test.zip")
	if err := Create(Options{Source: source, Destination: destination, Format: FormatZip}); err != nil {
		t.Fatalf("创建 Windows 发布归档失败：%v", err)
	}
	reader, err := zip.OpenReader(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	found := false
	for _, item := range reader.File {
		if item.Name == "web/index.html" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Windows 发布归档缺少 Web 入口")
	}
}

func TestCreateRejectsExistingArchive(t *testing.T) {
	source := createFixture(t)
	destination := filepath.Join(t.TempDir(), "existing.tar.gz")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Create(Options{Source: source, Destination: destination, Format: FormatTarGz})
	if err == nil || !strings.Contains(err.Error(), "拒绝覆盖") {
		t.Fatalf("已有发布归档未被拒绝：%v", err)
	}
	data, readErr := os.ReadFile(destination)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("已有发布归档被修改：data=%q err=%v", data, readErr)
	}
}

func TestCreateRejectsMissingExecutable(t *testing.T) {
	source := createFixture(t)
	err := Create(Options{
		Source: source, Destination: filepath.Join(t.TempDir(), "missing.tar.gz"),
		Format: FormatTarGz, Executables: []string{"missing"},
	})
	if err == nil || !strings.Contains(err.Error(), "缺少可执行文件") {
		t.Fatalf("缺少可执行文件未被拒绝：%v", err)
	}
}

func TestCreateRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 测试环境通常不允许普通用户创建符号链接")
	}
	source := createFixture(t)
	if err := os.Symlink(filepath.Join(source, "scert"), filepath.Join(source, "linked")); err != nil {
		t.Fatal(err)
	}
	err := Create(Options{
		Source: source, Destination: filepath.Join(t.TempDir(), "linked.tar.gz"), Format: FormatTarGz,
	})
	if err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("发布目录符号链接未被拒绝：%v", err)
	}
}

func createFixture(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install-linux.sh", "scert", "scert-admin", "scert-nginx", "ScertDesktop"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "web", "index.html"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}

func assertMode(t *testing.T, modes map[string]int64, name string, expected int64) {
	t.Helper()
	if actual, ok := modes[name]; !ok || actual != expected {
		t.Fatalf("归档权限不正确 %s：actual=%o expected=%o", name, actual, expected)
	}
}
