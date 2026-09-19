package nginxactivation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type coordinatedTestUpstream struct {
	mu       sync.Mutex
	current  ManagedCertificate
	readErr  error
	activate func(CertificateFiles, string) error
}

func (u *coordinatedTestUpstream) WithCertificate(ctx context.Context, operation func(ManagedSession) error) error {
	if !u.mu.TryLock() {
		return errors.New("测试上游正被独占")
	}
	defer u.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation(u)
}

func (u *coordinatedTestUpstream) Snapshot() (ManagedCertificate, error) {
	return u.current, u.readErr
}

func (u *coordinatedTestUpstream) Activate(files CertificateFiles, hash string) error {
	return u.activate(files, hash)
}

type coordinatedTestProxy struct {
	ProxyClient
	stage  func(context.Context, []byte, []byte) (string, error)
	commit func(context.Context, string) error
}

func (p coordinatedTestProxy) Stage(ctx context.Context, chain, key []byte) (string, error) {
	if p.stage != nil {
		return p.stage(ctx, chain, key)
	}
	return p.ProxyClient.Stage(ctx, chain, key)
}

func (p coordinatedTestProxy) Commit(ctx context.Context, id string) error {
	if p.commit != nil {
		return p.commit(ctx, id)
	}
	return p.ProxyClient.Commit(ctx, id)
}

type coordinatorFixture struct {
	*rotationFixture
	coordinator Coordinator
	session     *coordinatedTestUpstream
	previous    ManagedCertificate
	candidate   ManagedCertificate
}

func newCoordinatorFixture(t *testing.T) *coordinatorFixture {
	t.Helper()
	f := &coordinatorFixture{rotationFixture: newRotationFixture(t)}
	materials := t.TempDir()
	files := CertificateFiles{CertificatePath: filepath.Join(materials, "candidate.pem"), PrivateKeyPath: filepath.Join(materials, "candidate.key")}
	writeTestFile(t, files.CertificatePath, f.rotationFixture.candidate.chain)
	writeTestFile(t, files.PrivateKeyPath, f.rotationFixture.candidate.key)
	f.previous = ManagedCertificate{
		CertificateFiles: CertificateFiles{CertificatePath: filepath.Join(materials, "old.pem"), PrivateKeyPath: filepath.Join(materials, "old.key")},
		Hostname:         testHostname, Mode: "proxy", LeafHash: leafHash(f.old.pair.Certificate[0]),
		ProxyAddress: f.proxy.Address, UpstreamAddress: f.upstream.Address,
	}
	writeTestFile(t, f.previous.CertificatePath, f.old.chain)
	writeTestFile(t, f.previous.PrivateKeyPath, f.old.key)
	f.candidate = f.previous
	f.candidate.CertificateFiles, f.candidate.LeafHash = files, leafHash(f.rotationFixture.candidate.pair.Certificate[0])
	f.session = &coordinatedTestUpstream{current: f.previous}
	f.session.activate = func(actual CertificateFiles, hash string) error {
		if actual != files || hash != f.candidate.LeafHash {
			return errors.New("协调器未固定候选材料")
		}
		f.session.current = f.candidate
		f.upstream.active.Store(&f.rotationFixture.candidate.pair)
		return nil
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	f.coordinator = Coordinator{Directory: directory, Proxy: f.rotation, Upstream: f.session}
	return f
}

func (f *coordinatorFixture) record(t *testing.T) coordinationRecord {
	t.Helper()
	root, err := os.OpenRoot(f.coordinator.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	value, err := readCoordination(root)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (f *coordinatorFixture) save(t *testing.T, record coordinationRecord) {
	t.Helper()
	root, err := os.OpenRoot(f.coordinator.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := saveCoordination(root, record); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCommitsOverLocalIPC(t *testing.T) {
	f := newCoordinatorFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid()))
	defer stopLocalService(t, cancel, done)
	f.coordinator.Proxy = client
	if err := f.coordinator.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.coordinator.Activate(t.Context(), f.candidate.CertificateFiles); err != nil {
		t.Fatal(err)
	}
	if record := f.record(t); record.Phase != "committed" || record.ProxyID == "" {
		t.Fatal("双层提交未持久化", record.Phase)
	}
	if err := f.coordinator.Ready(t.Context()); err != nil {
		t.Fatal("提交后的双层入口未就绪", err)
	}
	if err := f.coordinator.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.reloader.reloadCount != 1 {
		t.Fatal("已完成事务不应再次重载")
	}
}

func TestCoordinatorRecoversLostResponses(t *testing.T) {
	for _, operation := range []string{"stage", "commit"} {
		t.Run(operation, func(t *testing.T) {
			f := newCoordinatorFixture(t)
			proxy := coordinatedTestProxy{ProxyClient: f.rotation}
			if operation == "stage" {
				proxy.stage = func(ctx context.Context, chain, key []byte) (string, error) {
					if _, err := f.rotation.Stage(ctx, chain, key); err != nil {
						return "", err
					}
					return "", errors.New("注入暂存应答丢失")
				}
			} else {
				proxy.commit = func(ctx context.Context, id string) error {
					if err := f.rotation.Commit(ctx, id); err != nil {
						return err
					}
					return errors.New("注入提交应答丢失")
				}
			}
			f.coordinator.Proxy = proxy
			err := f.coordinator.Activate(t.Context(), f.candidate.CertificateFiles)
			want := "committed"
			if operation == "stage" {
				want = "rolled_back"
				if err == nil || f.session.current != f.previous {
					t.Fatal("暂存应答丢失后不应切换上游", err)
				}
			} else if err != nil || f.session.current != f.candidate {
				t.Fatal("应验证已提交的同一事务", err)
			}
			if record := f.record(t); record.Phase != want || record.ProxyID == "" {
				t.Fatal("应答丢失后的终态错误", record.Phase)
			}
		})
	}
}

func TestCoordinatorRecoversRestartWindows(t *testing.T) {
	for _, phase := range []string{"prepared", "staged", "saved_before_snapshot", "upstream", "commit_before_record"} {
		t.Run(phase, func(t *testing.T) {
			f := newCoordinatorFixture(t)
			record := coordinationRecord{Version: 1, Phase: "prepared", Previous: f.previous, Candidate: f.candidate}
			f.save(t, record)
			id, err := f.rotation.Stage(t.Context(), f.rotationFixture.candidate.chain, f.rotationFixture.candidate.key)
			if err != nil {
				t.Fatal(err)
			}
			want := "rolled_back"
			if phase != "prepared" {
				record.Phase, record.ProxyID = "staged", id
				f.save(t, record)
			}
			if phase == "saved_before_snapshot" {
				f.session.readErr = errors.New("配置已保存但运行快照仍旧")
				if err := f.coordinator.Recover(t.Context()); !errors.Is(err, ErrCoordinationPending) {
					t.Fatal("混合状态下不能回退或提交", err)
				}
				f.assertStatus(t, "staged")
				f.session.readErr = nil
			}
			if phase == "saved_before_snapshot" || phase == "upstream" || phase == "commit_before_record" {
				if err := f.session.Activate(f.candidate.CertificateFiles, f.candidate.LeafHash); err != nil {
					t.Fatal(err)
				}
				want = "committed"
			}
			if phase == "upstream" || phase == "commit_before_record" {
				record.Phase = "upstream"
				f.save(t, record)
			}
			if phase == "commit_before_record" {
				if err := f.rotation.Commit(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			// 新实例只依靠同一目录的持久化记录和重启后实际提供的证书。
			restarted := Coordinator{Directory: f.coordinator.Directory, Proxy: f.rotation, Upstream: f.session}
			if err := restarted.Recover(t.Context()); err != nil {
				t.Fatal(err)
			}
			if record := f.record(t); record.Phase != want || record.ProxyID != id {
				t.Fatal("重启恢复终态错误", record.Phase)
			}
		})
	}
}

func TestCoordinatorKeepsUnobservedStagePending(t *testing.T) {
	f := newCoordinatorFixture(t)
	calls := 0
	f.coordinator.Proxy = coordinatedTestProxy{ProxyClient: f.rotation, stage: func(context.Context, []byte, []byte) (string, error) {
		calls++
		return "", errors.New("暂存请求是否到达尚不可知")
	}}
	if err := f.coordinator.Activate(t.Context(), f.candidate.CertificateFiles); !errors.Is(err, ErrCoordinationPending) {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.coordinator.Ready(t.Context()); !errors.Is(err, ErrCoordinationPending) {
			t.Fatal("未观测到事务不能允许再次申请", err)
		}
		if err := f.coordinator.Activate(t.Context(), f.candidate.CertificateFiles); !errors.Is(err, ErrCoordinationPending) {
			t.Fatal("未观测到事务不能重放暂存", err)
		}
	}
	if calls != 1 || f.record(t).Phase != "prepared" {
		t.Fatal("不能重放请求或清除不确定意图")
	}
	if _, err := f.rotation.Stage(t.Context(), f.rotationFixture.candidate.chain, f.rotationFixture.candidate.key); err != nil {
		t.Fatal(err)
	}
	if err := f.coordinator.Recover(t.Context()); err != nil {
		t.Fatal("迟到事务应在观测后恢复", err)
	}
	if f.record(t).Phase != "rolled_back" {
		t.Fatal("迟到暂存没有回退")
	}
}

func TestCoordinatorRefusesUnrelatedStateAndPolicy(t *testing.T) {
	for _, change := range []string{"hostname", "proxy", "upstream", "leaf", "mode"} {
		t.Run(change, func(t *testing.T) {
			f := newCoordinatorFixture(t)
			switch change {
			case "hostname":
				f.session.current.Hostname = "other.example.test"
			case "proxy":
				f.session.current.ProxyAddress = "127.0.0.1:1"
			case "upstream":
				f.session.current.UpstreamAddress = "127.0.0.1:2"
			case "leaf":
				f.session.current.LeafHash = f.candidate.LeafHash
			case "mode":
				f.session.current.Mode = "direct"
			}
			if err := f.coordinator.Ready(t.Context()); err == nil {
				t.Fatal("错误策略不能通过签发前检查")
			}
		})
	}
	f := newCoordinatorFixture(t)
	f.save(t, coordinationRecord{Version: 1, Phase: "prepared", Previous: f.previous, Candidate: f.candidate})
	f.session.current.Mode = "direct"
	if err := f.coordinator.Recover(t.Context()); !errors.Is(err, ErrCoordinationPending) {
		t.Fatal("不能因模式切换忽略未决事务", err)
	}
}

func TestCoordinatorSerializesWholeLifecycle(t *testing.T) {
	f := newCoordinatorFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	f.reloader.check = func(number int) error {
		if number == 2 {
			close(started)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { done <- f.coordinator.Activate(ctx, f.candidate.CertificateFiles) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("协调未开始")
	}
	second := f.coordinator
	if err := second.Recover(ctx); err == nil {
		t.Fatal("独立协调实例不能在生命周期中途恢复")
	}
	if err := second.Ready(ctx); err == nil {
		t.Fatal("独立协调实例不能开始下一次申请")
	}
	go func() { release <- struct{}{} }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
