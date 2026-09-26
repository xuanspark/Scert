package certstorage

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStoragePolicyRejectsSystemPathsAndInjection(t *testing.T) {
	for _, path := range []string{"/", "relative", "/ssl/../etc", "/ssl/link/../../etc", "/ssl/new\nReadWritePaths=/", "/ssl/a b", "/ssl/%n", "/etc/scert", "/scert/conf", "/var/lib/mysql", "/other/new"} {
		if validatePath(path, []string{"/ssl", "/var/lib/scert/certificates"}) == nil {
			t.Fatalf("危险路径被接受：%q", path)
		}
	}
	for _, path := range []string{"/ssl/new", "/ssl/a/b", "/var/lib/scert/certificates/new"} {
		if err := validatePath(path, []string{"/ssl", "/var/lib/scert/certificates"}); err != nil {
			t.Fatal(err)
		}
	}
	if validatePath("/etc/unsafe", []string{"/etc"}) == nil {
		t.Fatal("策略不能放开系统目录")
	}
}

func TestPrivilegedDirectoryPreparation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("需要隔离的 Linux root 测试环境")
	}
	// /tmp 本身可被所有用户写入，不能用作特权遍历的可信父目录。
	base, err := os.MkdirTemp("/root", "scert-storage-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	path := filepath.Join(base, "new", "certificates")
	if err := prepareDirectory(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	var info unix.Stat_t
	if err := unix.Stat(path, &info); err != nil || info.Uid != 65534 || info.Gid != 65534 || info.Mode&0o777 != 0o700 {
		t.Fatalf("目录权限不正确：%+v %v", info, err)
	}
	if err := prepareDirectory(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err := prepareDirectory(base, 65534, 65534); err == nil {
		t.Fatal("接管了已有 root 目录")
	}
	if err := os.Symlink("/etc", filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := prepareDirectory(filepath.Join(base, "link", "scert-test"), 65534, 65534); err == nil {
		t.Fatal("跟随了符号链接")
	}
	if err := prepareDirectory(filepath.Join(path, "nested"), 65534, 65534); err == nil {
		t.Fatal("特权操作接受了非 root 可写父目录")
	}
}
