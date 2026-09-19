package nginxactivation

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestActivationDirectoryTrust(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectory(base); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o755, 0o770, 0o777, 0o500} {
		if err := os.Chmod(base, mode); err != nil {
			t.Fatal(err)
		}
		if err := validateDirectory(base); err == nil {
			t.Fatalf("不应接受目录权限 %o", mode)
		}
	}
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "writable")
	child := filepath.Join(parent, "activation")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectory(child); err == nil {
		t.Fatal("不应接受可由其他用户写入的父目录")
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, "relative", "/", base + "/../escape", base + ";injection"} {
		if err := validateDirectory(path); err == nil {
			t.Fatalf("不应接受目录 %q", path)
		}
	}
}

func TestPrivateDirectoryKeepsTrustWithoutNginxSyntax(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"管理证书", "application data", "literal;directory"} {
		directory := filepath.Join(base, name)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := validatePrivateDirectory(directory); err != nil {
			t.Fatalf("应用私有状态目录不应套用 Nginx 指令字符限制：%v", err)
		}
		if err := validateDirectory(directory); err == nil {
			t.Fatal("实际写入 Nginx 配置的目录仍须限制字符")
		}
		if err := os.Chmod(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := validatePrivateDirectory(directory); err == nil {
			t.Fatal("应用状态目录仍须保持私有权限")
		}
	}
	parent := filepath.Join(base, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(child, link); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{child, link, "relative", "/", base + "/../escape"} {
		if err := validatePrivateDirectory(directory); err == nil {
			t.Fatalf("不应接受不可信应用状态目录：%q", directory)
		}
	}
}

func TestActivationFilesRejectLinksAndSpecialFiles(t *testing.T) {
	base := t.TempDir()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeExclusive(root, "state", []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(root, "state", []byte("overwrite")); !errors.Is(err, os.ErrExist) {
		t.Fatal("独占写入不应覆盖文件", err)
	}
	if _, err := readRegular(root, "state", 2); err == nil {
		t.Fatal("不应读取超限文件")
	}
	if err := root.Symlink("state", "symlink"); err != nil {
		t.Fatal(err)
	}
	if err := root.Link("state", "hardlink"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(base, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir("directory", 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"symlink", "hardlink", "state", "fifo", "directory"} {
		if _, err := readRegular(root, path, 4096); err == nil {
			t.Fatalf("不应读取不可信文件 %s", path)
		}
		if err := writeAtomic(root, path, []byte("replaced")); err == nil {
			t.Fatalf("不应替换不可信文件 %s", path)
		}
	}
	content, err := os.ReadFile(filepath.Join(base, "state"))
	if err != nil || string(content) != "original" {
		t.Fatal("被拒绝的操作改变了原文件", err)
	}
	if err := writeExclusive(root, "writable", []byte("untrusted")); err != nil {
		t.Fatal(err)
	}
	if err := root.Chmod("writable", 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegular(root, "writable", 4096); err == nil {
		t.Fatal("不应读取可由其他用户修改的状态")
	}
}

func TestActivationAtomicWritePersistsPrivateState(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, content := range [][]byte{[]byte("first"), []byte("second")} {
		if err := writeAtomic(root, "state", content); err != nil {
			t.Fatal(err)
		}
		actual, err := readRegular(root, "state", 4096)
		if err != nil || !bytes.Equal(actual, content) {
			t.Fatal("原子写入内容不一致", err)
		}
		info, err := root.Stat("state")
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("状态文件权限不正确", err)
		}
	}
}
