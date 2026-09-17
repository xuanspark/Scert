package nginxactivation

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/processlock"
)

type testReloader struct {
	checkCount  int
	reloadCount int
	check       func(int) error
	reload      func(int) error
}

func (r *testReloader) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.checkCount++
	if r.check != nil {
		return r.check(r.checkCount)
	}
	return nil
}

func (r *testReloader) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.reloadCount++
	if r.reload != nil {
		return r.reload(r.reloadCount)
	}
	return nil
}

type rotationFixture struct {
	rotation  Rotation
	authority testAuthority
	old       testMaterial
	candidate testMaterial
	proxy     *testEndpoint
	upstream  *testEndpoint
	reloader  *testReloader
	initial   []byte
}

func writeTestFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newRotationFixture(t *testing.T) *rotationFixture {
	t.Helper()
	a := newAuthority(t)
	now := time.Now()
	f := &rotationFixture{authority: a}
	f.old = a.issue(t, testHostname, now.Add(-time.Hour), now.Add(24*time.Hour))
	f.candidate = a.issue(t, testHostname, now.Add(-time.Hour), now.Add(48*time.Hour))
	f.proxy = newTestEndpoint(t, a, f.old, nil)
	f.upstream = newTestEndpoint(t, a, f.old, nil)
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	id := "00000000000000000000000000000001"
	if err := os.Mkdir(filepath.Join(directory, id), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, id, "fullchain.pem"), f.old.chain)
	writeTestFile(t, filepath.Join(directory, id, "key.pem"), f.old.key)
	f.initial = renderInclude(directory, id)
	writeTestFile(t, filepath.Join(directory, includeName), f.initial)
	f.reloader = &testReloader{}
	f.rotation = Rotation{Directory: directory, Proxy: f.proxy.Endpoint, Upstream: f.upstream.Endpoint, Reloader: f.reloader}
	f.reloader.reload = func(int) error {
		active, err := os.ReadFile(filepath.Join(directory, includeName))
		if err != nil {
			return err
		}
		if bytes.Equal(active, f.initial) {
			f.proxy.active.Store(&f.old.pair)
		} else {
			f.proxy.active.Store(&f.candidate.pair)
		}
		return nil
	}
	return f
}

func (f *rotationFixture) assertStatus(t *testing.T, status string) transaction {
	t.Helper()
	root, err := os.OpenRoot(f.rotation.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	current, err := readTransaction(root)
	if err != nil || current.Status != status {
		t.Fatalf("status=%s, want=%s, error=%v", current.Status, status, err)
	}
	return current
}

func TestRotationStageCommitAndRecover(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()
	id, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key)
	if err != nil {
		t.Fatal(err)
	}
	f.assertStatus(t, "staged")
	if _, err := f.upstream.Check(ctx, f.old.pair.Certificate[0]); err != nil {
		t.Fatal("暂存不应改变上游", err)
	}
	if _, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key); err == nil {
		t.Fatal("存在未决事务时不应再次暂存")
	}
	if err := f.rotation.Commit(ctx, "wrong"); err == nil {
		t.Fatal("错误事务编号不应提交")
	}
	deadline, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if err := f.rotation.Commit(deadline, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("双层不一致不应提交：%v", err)
	}
	f.assertStatus(t, "staged")
	f.upstream.active.Store(&f.candidate.pair)
	if err := f.rotation.Commit(ctx, id); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(t, "committed")
	reloads := f.reloader.reloadCount
	if err := f.rotation.Recover(ctx); err != nil || f.reloader.reloadCount != reloads {
		t.Fatalf("恢复不应回退已提交事务：%v", err)
	}
	if err := f.rotation.Rollback(ctx, id); err == nil {
		t.Fatal("不应回退已提交事务")
	}
}

func TestRotationFailuresRestorePreviousCertificate(t *testing.T) {
	for _, scenario := range []string{"check", "reload", "probe", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRotationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
			defer cancel()
			originalReload := f.reloader.reload
			f.reloader.check = func(number int) error {
				if scenario == "check" && number == 2 {
					return errors.New("注入候选配置检查失败")
				}
				return nil
			}
			f.reloader.reload = func(number int) error {
				if number == 1 {
					switch scenario {
					case "reload":
						return errors.New("注入重载失败")
					case "probe":
						return nil
					case "cancel":
						cancel()
					}
				}
				return originalReload(number)
			}
			if _, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key); err == nil {
				t.Fatal("故障应导致暂存失败")
			}
			f.assertStatus(t, "rolled_back")
			if _, err := f.proxy.Check(context.Background(), f.old.pair.Certificate[0]); err != nil {
				t.Fatal("旧入口未恢复", err)
			}
		})
	}
}

func TestRotationRestartRecoveryAndExternalChanges(t *testing.T) {
	for _, scenario := range []string{"staged", "pending-before-switch", "pending-after-switch", "external-include", "external-material", "rollback-check"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRotationFixture(t)
			ctx := context.Background()
			id, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key)
			if err != nil {
				t.Fatal(err)
			}
			current := f.assertStatus(t, "staged")
			root, err := os.OpenRoot(f.rotation.Directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			switch scenario {
			case "pending-before-switch", "pending-after-switch":
				current.Status = "pending"
				if err := saveTransaction(root, current); err != nil {
					t.Fatal(err)
				}
				if scenario == "pending-before-switch" {
					writeTestFile(t, filepath.Join(f.rotation.Directory, includeName), f.initial)
					f.proxy.active.Store(&f.old.pair)
				}
			case "external-include":
				writeTestFile(t, filepath.Join(f.rotation.Directory, includeName), []byte("# 外部变更\n"))
			case "external-material":
				oldID, err := includeVersion(f.rotation.Directory, f.initial)
				if err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(f.rotation.Directory, oldID, "key.pem"), f.candidate.key)
			case "rollback-check":
				f.reloader.check = func(int) error { return errors.New("注入回退检查失败") }
			}
			restarted := f.rotation
			err = restarted.Recover(ctx)
			if scenario == "external-include" || scenario == "external-material" || scenario == "rollback-check" {
				if err == nil {
					t.Fatal("未完成回退不应记录成功")
				}
				f.assertStatus(t, "staged")
				if scenario == "external-include" {
					active, err := os.ReadFile(filepath.Join(f.rotation.Directory, includeName))
					if err != nil || string(active) != "# 外部变更\n" {
						t.Fatal("不应覆盖外部配置", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.assertStatus(t, "rolled_back")
			if err := restarted.Rollback(ctx, id); err != nil {
				t.Fatal("重复回退应安全", err)
			}
		})
	}
}

func TestRotationRejectsInvalidCandidatesBeforeReload(t *testing.T) {
	for _, scenario := range []string{"expired", "future", "hostname", "untrusted", "upstream-trust", "key-mismatch", "unchanged-expiry", "large", "initial-mismatch", "disk-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRotationFixture(t)
			now := time.Now()
			material := f.candidate
			switch scenario {
			case "expired":
				material = f.authority.issue(t, testHostname, now.Add(-48*time.Hour), now.Add(-time.Hour))
			case "future":
				material = f.authority.issue(t, testHostname, now.Add(time.Hour), now.Add(72*time.Hour))
			case "hostname":
				material = f.authority.issue(t, "other.example.test", now.Add(-time.Hour), now.Add(72*time.Hour))
			case "untrusted":
				material = newAuthority(t).issue(t, testHostname, now.Add(-time.Hour), now.Add(72*time.Hour))
			case "upstream-trust":
				f.rotation.Upstream.Roots = x509.NewCertPool()
			case "key-mismatch":
				material.key = f.old.key
			case "unchanged-expiry":
				material = f.old
			case "large":
				material.chain = make([]byte, (1<<20)+1)
			case "initial-mismatch":
				f.upstream.active.Store(&f.candidate.pair)
			case "disk-mismatch":
				id, err := includeVersion(f.rotation.Directory, f.initial)
				if err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(f.rotation.Directory, id, "fullchain.pem"), f.candidate.chain)
			}
			if _, err := f.rotation.Stage(context.Background(), material.chain, material.key); err == nil {
				t.Fatal("不应接受无效候选或不一致基线")
			}
			if f.reloader.checkCount != 0 || f.reloader.reloadCount != 0 {
				t.Fatal("预检失败不应调用重载器")
			}
			if _, err := os.Stat(filepath.Join(f.rotation.Directory, journalName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("预检失败不应开始事务", err)
			}
		})
	}
}

func TestRotationLockAndCorruptJournal(t *testing.T) {
	f := newRotationFixture(t)
	lock, err := processlock.Acquire(filepath.Join(f.rotation.Directory, ".activation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.rotation.Stage(context.Background(), f.candidate.chain, f.candidate.key); !errors.Is(err, processlock.ErrLocked) {
		t.Fatalf("并发事务未被拒绝：%v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.rotation.Directory, journalName), []byte(`{"status":"staged"}`))
	if err := f.rotation.Recover(context.Background()); err == nil {
		t.Fatal("损坏事务不应触发恢复")
	}
	if f.reloader.reloadCount != 0 {
		t.Fatal("损坏事务不应调用重载器")
	}
}

func TestRotationPreservesConcurrentIncludeChange(t *testing.T) {
	for _, checkNumber := range []int{1, 2} {
		t.Run(fmt.Sprint(checkNumber), func(t *testing.T) {
			f := newRotationFixture(t)
			external := []byte("# 管理员更新\n")
			f.reloader.check = func(number int) error {
				if number == checkNumber {
					writeTestFile(t, filepath.Join(f.rotation.Directory, includeName), external)
				}
				return nil
			}
			if _, err := f.rotation.Stage(context.Background(), f.candidate.chain, f.candidate.key); err == nil {
				t.Fatal("外部变更应阻止切换")
			}
			actual, err := os.ReadFile(filepath.Join(f.rotation.Directory, includeName))
			if err != nil || !bytes.Equal(actual, external) || f.reloader.reloadCount != 0 {
				t.Fatal("不应覆盖或重载外部变更", err)
			}
		})
	}
}

func TestRotationJournalFailureRemainsRecoverable(t *testing.T) {
	f := newRotationFixture(t)
	originalReload := f.reloader.reload
	f.reloader.reload = func(number int) error {
		if number == 1 {
			if err := os.Chmod(filepath.Join(f.rotation.Directory, journalName), 0o666); err != nil {
				return err
			}
		}
		return originalReload(number)
	}
	if _, err := f.rotation.Stage(context.Background(), f.candidate.chain, f.candidate.key); err == nil {
		t.Fatal("日志持久化失败不应返回成功")
	}
	if _, err := f.proxy.Check(context.Background(), f.old.pair.Certificate[0]); err != nil {
		t.Fatal("日志失败后仍应恢复旧入口", err)
	}
	if err := os.Chmod(filepath.Join(f.rotation.Directory, journalName), 0o600); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(t, "pending")
	if err := f.rotation.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(t, "rolled_back")
}

func TestRotationCrossProcessLock(t *testing.T) {
	if path := os.Getenv("SCERT_TEST_ACTIVATION_LOCK"); path != "" {
		lock, err := processlock.Acquire(path)
		if os.Getenv("SCERT_TEST_EXPECT_LOCKED") == "1" {
			if lock != nil {
				_ = lock.Close()
			}
			if !errors.Is(err, processlock.ErrLocked) {
				t.Fatalf("其他进程持有的锁未生效：%v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), ".activation.lock")
	lock, err := processlock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"1", "0"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestRotationCrossProcessLock$")
		command.Env = append(os.Environ(), "SCERT_TEST_ACTIVATION_LOCK="+path, "SCERT_TEST_EXPECT_LOCKED="+expected)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("跨进程锁验证失败：%v\n%s", err, output)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
