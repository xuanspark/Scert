package releasearchive

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryIgnoreBoundaries(t *testing.T) {
	root, sandbox := repositoryIgnoreFixture(t)
	ignored := []string{
		"scert.exe", "nested/scert.exe", "nested/lib.dll", "nested/lib.so", "nested/lib.dylib",
		"Scert.tar.gz", "nested/release.tgz", "nested/release.zip",
		".runtime/config.json", "nested/.runtime/state.json", ".deploy-stage/config.json",
		"build/release/manifest.json", "release/candidate/manifest.json", "artifacts/task/result.json",
		"backend/bin/scert", "backend/build/scert", "frontend/node_modules/pkg/index.js",
		"desktop/frontend/node_modules/pkg/index.js", "nested/dist/index.html", "frontend/dist-release-review/index.html",
		"desktop/frontend/dist/index.html", "desktop/bin/ScertDesktop", "desktop/build/app", "desktop/frontend/wailsjs/runtime.js",
		"nested/coverage/report.json", "nested/.vite/cache.json", "nested/app.log",
		"fix.patch", "nested/fix.patch", "nested/file.orig", "nested/file.rej", "nested/file.tmp", "nested/file.swp",
		"nested/__pycache__/test.pyc", "nested/.pytest_cache/state", "nested/.cache/state",
		"nested/.playwright/state", "nested/playwright-report/index.html", "nested/test-results/result.json",
		"browser-profile/Default/Cookies", "chrome-profile/Default/Cookies",
		"nested/data.db", "nested/data.sqlite", "nested/data.sqlite3", "nested/data.dump",
		"nested/data.db-wal", "nested/data.db-shm", "nested/data.db-journal",
		"nested/data.sqlite-wal", "nested/data.sqlite-shm", "nested/data.sqlite-journal",
		"nested/data.sqlite3-wal", "nested/data.sqlite3-shm", "nested/data.sqlite3-journal",
		".env", "nested/.env.production", "nested/tls.key", "nested/cert.p12", "nested/cert.pfx",
		"nested/id_rsa", "nested/id_dsa", "nested/id_ecdsa", "nested/id_ed25519",
		"nested/tls.private.pem", "nested/tls-key.pem", "nested/tls_key.pem",
	}
	kept := []string{
		".gitignore", "backend/go.mod", "backend/go.sum", "backend/cmd/scert/main.go",
		"backend/internal/database/migrations/sqlite/0001_initial.sql",
		"backend/internal/certificate/trust/staging-x1.pem", "backend/internal/certificate/trust/staging-x2.pem",
		"backend/internal/certificate/trust/staging-ye.pem", "backend/internal/certificate/trust/staging-yr.pem",
		"backend/internal/releasearchive/source_boundary_test.go", "frontend/package-lock.json",
		"frontend/src/views/Login/Login.vue", "frontend/src/views/Login/Login.test.js",
		"deploy/install-linux.sh", "deploy/install-windows.ps1", "deploy/tests/install-linux-test.py",
		"desktop/main.go", "build/README.md", "build/build-release.ps1",
		"build/toolchain.mjs", "build/toolchain.test.mjs", "frontend/.npmrc",
		"release/README.md", "artifacts/README.md", "tests/README.md", "scripts/README.md",
		"tests/example_test.go", "scripts/example.go", ".env.example", "nested/.env.sample",
	}
	// 忽略规则不能自动移出已跟踪产物，必须另外核对当前索引。
	command := repositoryGitCommand(root, "ls-files", "-z")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("读取当前源码索引失败：%v", err)
	}
	var tracked []string
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) != 0 {
			tracked = append(tracked, string(path))
		}
	}
	paths := append(append(append([]string{}, ignored...), kept...), tracked...)
	actual := repositoryIgnoredPaths(t, sandbox, paths)
	for _, path := range ignored {
		t.Run("ignored/"+path, func(t *testing.T) {
			if !actual[path] {
				t.Errorf("产物或敏感文件未被忽略：%s", path)
			}
		})
	}
	for _, path := range kept {
		t.Run("kept/"+path, func(t *testing.T) {
			if actual[path] {
				t.Errorf("正式源码或说明被误忽略：%s", path)
			}
		})
	}

	for _, path := range tracked {
		if actual[path] {
			t.Errorf("当前索引仍包含应排除文件：%s", path)
		}
	}
}

func TestRepositoryCheckoutLineEndings(t *testing.T) {
	root, sandbox := repositoryIgnoreFixture(t)
	attributes, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, ".gitattributes"), attributes, 0o600); err != nil {
		t.Fatal(err)
	}
	text := []byte("first line\nsecond line\n")
	files := map[string][]byte{
		"backend/go.mod":             text,
		"backend/go.sum":             text,
		"frontend/package.json":      text,
		"frontend/package-lock.json": text,
		"frontend/.npmrc":            text,
		"frontend/index.html":        text,
		"frontend/src/App.vue":       text,
		"frontend/src/App.js":        text,
		"frontend/src/App.css":       text,
		"build/toolchain.mjs":        text,
		"build/build-release.ps1":    text,
		"frontend/logo/fixture.png":  {0x89, 0, 13, 10, 0xff},
	}
	arguments := []string{"add", "--", ".gitattributes"}
	for path, data := range files {
		fullPath := filepath.Join(sandbox, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		arguments = append(arguments, path)
	}
	if output, err := repositoryGitCommand(sandbox, arguments...).CombinedOutput(); err != nil {
		t.Fatalf("准备换行测试索引失败：%v\n%s", err, output)
	}
	// 从相同索引实际签出，证明用户换行设置不会改变文本，也不会转换二进制。
	for _, value := range []string{"true", "false", "input"} {
		t.Run(value, func(t *testing.T) {
			destination := t.TempDir()
			command := repositoryGitCommand(sandbox, "-c", "core.autocrlf="+value,
				"checkout-index", "--all", "--prefix="+filepath.ToSlash(destination)+"/")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("签出换行测试源码失败：%v\n%s", err, output)
			}
			for path, expected := range files {
				actual, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(path)))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(actual, expected) {
					t.Errorf("签出改变了构建输入或二进制内容：%s", path)
				}
			}
		})
	}
}

func repositoryIgnoreFixture(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("源码边界测试需要 Git，不能跳过后视为通过")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	sandbox := t.TempDir()
	command := repositoryGitCommand(sandbox, "init", "--quiet")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("初始化隔离 Git 仓库失败：%v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(sandbox, ".gitignore"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// 子目录例外也参与核对，防止后续规则重新放行根级禁止的产物。
	output, err := repositoryGitCommand(root, "ls-files", "-z", "--", "*.gitignore").Output()
	if err != nil {
		t.Fatalf("读取子目录忽略规则失败：%v", err)
	}
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) == 0 || string(path) == ".gitignore" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, string(path)))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(sandbox, string(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, sandbox
}

func repositoryIgnoredPaths(t *testing.T, sandbox string, paths []string) map[string]bool {
	t.Helper()
	// 使用空字节分隔批量路径，既兼容空格和中文，也避免逐文件启动 Git。
	command := repositoryGitCommand(sandbox, "check-ignore", "--no-index", "--stdin", "-z")
	command.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	output, err := command.CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Fatalf("检查忽略规则失败：%v\n%s", err, output)
		}
	}
	result := make(map[string]bool)
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) != 0 {
			result[string(path)] = true
		}
	}
	return result
}

func repositoryGitCommand(directory string, arguments ...string) *exec.Cmd {
	command := exec.Command("git", append([]string{"-c", "core.excludesFile=" + os.DevNull, "-c", "init.templateDir="}, arguments...)...)
	command.Dir = directory
	// 不继承调用者指定的仓库、索引、配置或全局排除文件。
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	return command
}
