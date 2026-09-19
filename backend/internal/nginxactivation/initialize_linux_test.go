package nginxactivation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/processlock"
	"golang.org/x/sys/unix"
)

func initializationFixture(t *testing.T) (*rotationFixture, string, string) {
	t.Helper()
	f := newRotationFixture(t)
	f.rotation.Directory = t.TempDir()
	if err := os.Chmod(f.rotation.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	material := t.TempDir()
	chain, key := filepath.Join(material, "fullchain.pem"), filepath.Join(material, "key.pem")
	writeTestFile(t, chain, f.old.chain)
	writeTestFile(t, key, f.old.key)
	return f, chain, key
}

func TestInitializeCreatesPrivateBaselineWithoutReload(t *testing.T) {
	f, chain, key := initializationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	id, err := f.rotation.Initialize(ctx, chain, key)
	if err != nil || !validID(id) {
		t.Fatal("初始化失败或版本编号无效", err)
	}
	if f.reloader.reloadCount != 0 || f.reloader.checkCount != 1 {
		t.Fatal("初始化只能检查既有配置，不能重载")
	}
	for path, mode := range map[string]os.FileMode{
		id: 0o700, filepath.Join(id, "fullchain.pem"): 0o600,
		filepath.Join(id, "key.pem"): 0o600, includeName: 0o600,
	} {
		info, err := os.Lstat(filepath.Join(f.rotation.Directory, path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("初始化材料权限错误：%s，%v", path, err)
		}
	}
	root, err := os.OpenRoot(f.rotation.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	include, err := readRegular(root, includeName, 4096)
	if err != nil || !bytes.Equal(include, renderInclude(f.rotation.Directory, id)) {
		t.Fatal("专用引用格式错误", err)
	}
	if err := verifyIncludeMaterial(root, include, f.old.pair.Certificate[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.rotation.Directory, journalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("初始化不应创建轮换事务", err)
	}
	if _, err := f.rotation.Initialize(ctx, chain, key); err == nil {
		t.Fatal("不能重复初始化已有状态")
	}
	if err := requireInclude(root, include); err != nil {
		t.Fatal("重复初始化改变了原引用", err)
	}
}

func TestInitializeRejectsUnsafeMaterial(t *testing.T) {
	for _, scenario := range []string{
		"key-readable", "key-writable", "parent-writable", "symlink", "hardlink", "fifo",
		"large", "mismatch", "different-leaf", "upstream-mismatch", "relative", "missing",
	} {
		t.Run(scenario, func(t *testing.T) {
			f, chain, key := initializationFixture(t)
			switch scenario {
			case "key-readable", "key-writable", "parent-writable":
				path, mode := key, os.FileMode(0o644)
				if scenario == "key-writable" {
					mode = 0o666
				}
				if scenario == "parent-writable" {
					path, mode = filepath.Dir(key), 0o777
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(key, key+".link"); err != nil {
					t.Fatal(err)
				}
				key += ".link"
			case "hardlink":
				if err := os.Link(key, key+".link"); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				key += ".fifo"
				if err := unix.Mkfifo(key, 0o600); err != nil {
					t.Fatal(err)
				}
			case "large":
				writeTestFile(t, chain, make([]byte, (1<<20)+1))
			case "mismatch":
				writeTestFile(t, key, f.candidate.key)
			case "different-leaf":
				writeTestFile(t, chain, f.candidate.chain)
				writeTestFile(t, key, f.candidate.key)
			case "upstream-mismatch":
				f.upstream.active.Store(&f.candidate.pair)
			case "relative":
				key = "key.pem"
			case "missing":
				key += ".missing"
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := f.rotation.Initialize(ctx, chain, key); err == nil {
				t.Fatal("不应接受不可信或不一致的初始化材料")
			}
			if f.reloader.checkCount != 0 || f.reloader.reloadCount != 0 {
				t.Fatal("材料预检失败不应执行 Nginx 命令")
			}
			assertInitializationEmpty(t, f.rotation.Directory)
		})
	}
}

func TestInitializePreservesExistingState(t *testing.T) {
	for _, scenario := range []string{"include", "transaction", "partial", "external-change", "config-failure", "cancel", "locked"} {
		t.Run(scenario, func(t *testing.T) {
			f, chain, key := initializationFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			name, content := "", []byte("保留原有状态\n")
			switch scenario {
			case "include", "transaction", "partial":
				name = map[string]string{"include": includeName, "transaction": journalName, "partial": ".pending-old"}[scenario]
				writeTestFile(t, filepath.Join(f.rotation.Directory, name), content)
			case "external-change":
				name = includeName
				f.reloader.check = func(int) error {
					writeTestFile(t, filepath.Join(f.rotation.Directory, name), content)
					return nil
				}
			case "config-failure":
				f.reloader.check = func(int) error { return errors.New("注入配置检查失败") }
			case "cancel":
				cancel()
			case "locked":
				lock, err := processlock.Acquire(filepath.Join(f.rotation.Directory, ".activation.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			}
			if _, err := f.rotation.Initialize(ctx, chain, key); err == nil {
				t.Fatal("已有状态、取消或故障不应初始化成功")
			}
			if f.reloader.reloadCount != 0 {
				t.Fatal("初始化不能重载 Nginx")
			}
			if name == "" {
				assertInitializationEmpty(t, f.rotation.Directory)
				return
			}
			actual, err := os.ReadFile(filepath.Join(f.rotation.Directory, name))
			if err != nil || !bytes.Equal(actual, content) {
				t.Fatal("初始化不能覆盖已有状态", err)
			}
		})
	}
}

func assertInitializationEmpty(t *testing.T, directory string) {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := requireUninitialized(root); err != nil {
		t.Fatal("预检失败不应留下证书或引用", err)
	}
}
