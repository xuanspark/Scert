package releasearchive

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestScanPathsRejectsEncodedLocalPaths(t *testing.T) {
	const local = `D:\Workspace\Scert`
	for name, value := range map[string]string{
		"native":       local + `\backend\main.go`,
		"forward":      `d:/workspace/scert/backend/main.go`,
		"json":         `D:\\Workspace\\Scert\\backend\\main.go`,
		"json_forward": `D:\/Workspace\/Scert\/backend\/main.go`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := scanPaths(strings.NewReader(value), pathPatterns([]string{local})); err == nil {
				t.Fatal("未拒绝本机路径")
			}
		})
	}
	for name, bigEndian := range map[string]bool{"utf16le": false, "utf16be": true} {
		t.Run(name, func(t *testing.T) {
			const unicodePath = `C:\用户\构建`
			var data []byte
			for _, unit := range utf16.Encode([]rune(unicodePath)) {
				if bigEndian {
					data = append(data, byte(unit>>8), byte(unit))
				} else {
					data = append(data, byte(unit), byte(unit>>8))
				}
			}
			if err := scanPaths(bytes.NewReader(data), pathPatterns([]string{unicodePath})); err == nil {
				t.Fatal("未拒绝 UTF-16 本机路径")
			}
		})
	}
}

func TestScanPathsRejectsPathsAcrossReadBoundary(t *testing.T) {
	reader := io.MultiReader(strings.NewReader("prefix C:/Use"), strings.NewReader("rs/build-user/project/main.go"))
	if err := scanPaths(reader, pathPatterns([]string{`C:\Users\build-user`})); err == nil {
		t.Fatal("读取边界上的本机路径被漏检")
	}
}

func TestScanPathsAllowsModuleAndRuntimePaths(t *testing.T) {
	data := "git.pcux.cn/xuans/Scert/backend@v1.0.0/cmd/scert/main.go\x00/scert/conf/scert.json\x00/etc/nginx/nginx.conf\x00PATH=/usr/sbin:/usr/bin:/sbin:/bin"
	if err := scanPaths(strings.NewReader(data), pathPatterns([]string{`D:\Workspace`, `C:\Users\builder`})); err != nil {
		t.Fatalf("模块路径或部署路径被误拒绝：%v", err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("读取失败") }

func TestScanPathsPropagatesReadError(t *testing.T) {
	if err := scanPaths(brokenReader{}, pathPatterns([]string{`C:\Users\builder`})); err == nil {
		t.Fatal("读取失败必须阻止发布")
	}
}

func TestVerifyRequiresTrimpathBuildSetting(t *testing.T) {
	for _, trimpath := range []bool{false, true} {
		name := "untrimmed"
		if trimpath {
			name = "trimmed"
		}
		t.Run(name, func(t *testing.T) {
			source := t.TempDir()
			mainPath := filepath.Join(t.TempDir(), "main.go")
			if err := os.WriteFile(mainPath, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			binary := "fixture"
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			flag := "-trimpath=false"
			if trimpath {
				flag = "-trimpath=true"
			}
			command := exec.Command("go", "build", flag, "-buildvcs=false", "-o", filepath.Join(source, binary), mainPath)
			command.Env = append(os.Environ(), "GOFLAGS=", "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("构建路径检查夹具失败：%v\n%s", err, output)
			}
			err := Verify(source, []string{binary}, []string{"unused-build-root-298e4699"})
			if trimpath && err != nil {
				t.Fatalf("裁剪产物检查失败：%v", err)
			}
			if !trimpath && (err == nil || !strings.Contains(err.Error(), "-trimpath")) {
				t.Fatalf("未拒绝缺少路径裁剪的构建：%v", err)
			}
			if trimpath {
				if err := Verify(source, []string{binary}, []string{filepath.Dir(mainPath)}); err != nil {
					t.Fatalf("裁剪构建仍有源目录残留：%v", err)
				}
			}
		})
	}
}

func TestVerifyChecksStaticAssetsAndRejectsInvalidInputs(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "app.js"), []byte(`const path="C:\\Users\\builder\\src";`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(source, []string{"missing"}, []string{`C:\Users\builder`}); err == nil || !strings.Contains(err.Error(), "本机构建路径") {
		t.Fatalf("静态文件路径残留未被拒绝：%v", err)
	}
	for name, test := range map[string]struct {
		binaries []string
		paths    []string
	}{
		"empty_binaries": {},
		"empty_paths":    {binaries: []string{"missing"}},
		"blank_paths":    {binaries: []string{"missing"}, paths: []string{" "}},
		"outside":        {binaries: []string{"../outside"}, paths: []string{"unused-local-root"}},
		"missing":        {binaries: []string{"missing"}, paths: []string{"unused-local-root"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(source, test.binaries, test.paths); err == nil {
				t.Fatal("无效发布检查输入未被拒绝")
			}
		})
	}
}

func TestReleaseScriptUsesPathChecksAndIncludesLinuxHelper(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "build", "build-release.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"go build -trimpath", "wails build -trimpath", "Test-ReleasePaths -Source $targetDirectory",
		`-Package "./cmd/scert-nginx"`, `$binaries += "scert-nginx"`,
		`-Executable (@("install-linux.sh") + $binaries)`,
		`trimpath = $true`, `local_path_check = "passed"`,
	} {
		if !strings.Contains(string(data), required) {
			t.Errorf("发布脚本缺少必要约束：%s", required)
		}
	}
}
