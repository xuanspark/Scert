package nginxactivation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/processlock"
)

func requestIDForTest(t *testing.T) string {
	t.Helper()
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRequestFencePersistsAcrossRestartAndNextTransaction(t *testing.T) {
	f := newRotationFixture(t)
	id := requestIDForTest(t)
	for range 2 {
		state, err := f.rotation.ResolveRequest(t.Context(), id)
		if err != nil || state.ID != id || state.Status != "canceled" {
			t.Fatal("未送达请求应持久化取消栅栏", state, err)
		}
	}
	nextID := requestIDForTest(t)
	if _, err := f.rotation.StageRequest(t.Context(), nextID, f.candidate.chain, f.candidate.key); err != nil {
		t.Fatal(err)
	}
	f.upstream.active.Store(&f.candidate.pair)
	if err := f.rotation.Commit(t.Context(), nextID); err != nil {
		t.Fatal(err)
	}
	restarted := f.rotation
	if _, err := restarted.StageRequest(t.Context(), id, f.candidate.chain, f.candidate.key); err == nil {
		t.Fatal("重启且后续事务提交后仍须拒绝旧请求")
	}
	if _, err := restarted.StageRequest(t.Context(), nextID, f.candidate.chain, f.candidate.key); err == nil {
		t.Fatal("已经提交的请求不能重放")
	}
	if f.reloader.reloadCount != 1 {
		t.Fatal("重复请求触发了额外重载")
	}
}

func TestRequestReservationFailureDoesNotActivate(t *testing.T) {
	for _, invalid := range []string{"symlink", "corrupt", "directory"} {
		t.Run(invalid, func(t *testing.T) {
			f := newRotationFixture(t)
			id := requestIDForTest(t)
			path := filepath.Join(f.rotation.Directory, "request-"+id+".json")
			switch invalid {
			case "symlink":
				if err := os.Symlink("missing", path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				writeTestFile(t, path, []byte(`{"status":"canceled"}`))
			}
			if _, err := f.rotation.StageRequest(t.Context(), id, f.candidate.chain, f.candidate.key); err == nil {
				t.Fatal("无法可信保存请求时不能激活")
			}
			if _, err := f.rotation.ResolveRequest(t.Context(), id); err == nil {
				t.Fatal("不能把损坏请求记录当成尚未执行")
			}
			if f.reloader.reloadCount != 0 {
				t.Fatal("请求记录故障不能重载代理")
			}
		})
	}
}

func TestRequestResolveCannotOvertakeActiveStage(t *testing.T) {
	f := newRotationFixture(t)
	id := requestIDForTest(t)
	started, release := make(chan struct{}), make(chan struct{})
	f.reloader.check = func(number int) error {
		if number == 1 {
			close(started)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		_, err := f.rotation.StageRequest(ctx, id, f.candidate.chain, f.candidate.key)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("暂存没有开始")
	}
	_, err := f.rotation.ResolveRequest(ctx, id)
	close(release)
	stageErr := <-done
	if !errors.Is(err, processlock.ErrLocked) || stageErr != nil {
		t.Fatal("恢复不能越过正在执行的暂存", err, stageErr)
	}
	state, err := f.rotation.ResolveRequest(ctx, id)
	if err != nil || state.ID != id || state.Status != "staged" {
		t.Fatal("已执行请求应返回对应事务", state, err)
	}
}

func TestRequestReservationBeforeTransactionCanBeCanceled(t *testing.T) {
	f := newRotationFixture(t)
	id := requestIDForTest(t)
	if _, err := f.rotation.StageRequest(t.Context(), id, []byte("invalid"), []byte("invalid")); err == nil {
		t.Fatal("无效材料必须拒绝")
	}
	state, err := f.rotation.ResolveRequest(t.Context(), id)
	if err != nil || state.Status != "canceled" {
		t.Fatal("已占位但未写入事务的请求应允许取消", state, err)
	}
	if _, err := f.rotation.StageRequest(t.Context(), id, f.candidate.chain, f.candidate.key); err == nil {
		t.Fatal("同一编号不能在失败后替换材料重试")
	}
}

func TestRequestResolveRefusesChangedBaseline(t *testing.T) {
	f := newRotationFixture(t)
	id := requestIDForTest(t)
	if _, err := f.rotation.StageRequest(t.Context(), id, []byte("invalid"), []byte("invalid")); err == nil {
		t.Fatal("无效材料必须拒绝")
	}
	if _, err := f.rotation.Stage(t.Context(), f.candidate.chain, f.candidate.key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rotation.ResolveRequest(t.Context(), id); err == nil {
		t.Fatal("基准事务已变化时不能推断旧占位尚未执行")
	}
	f.assertStatus(t, "staged")
	if f.reloader.reloadCount != 1 {
		t.Fatal("恢复查询不能改动无关事务")
	}
}

func TestRequestProtocolRejectsMissingIDAndExtraMaterial(t *testing.T) {
	id := requestIDForTest(t)
	for _, message := range []request{
		{Version: protocolVersion, Action: "stage_request", Chain: []byte("chain"), Key: []byte("key")},
		{Version: protocolVersion, Action: "stage_request", ID: "../request", Chain: []byte("chain"), Key: []byte("key")},
		{Version: protocolVersion, Action: "resolve_request"},
		{Version: protocolVersion, Action: "resolve_request", ID: id, Chain: []byte("chain")},
	} {
		if validRequest(message) {
			t.Fatal("无效请求不应通过协议检查", message.Action)
		}
	}
}
