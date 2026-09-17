package nginxactivation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotationReadyAndState(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()
	state, err := f.rotation.State(ctx)
	if err != nil || state.Status != "idle" {
		t.Fatalf("空事务状态错误：%+v, %v", state, err)
	}
	ready, err := f.rotation.Ready(ctx)
	if err != nil || ready.LeafHash != leafHash(f.old.pair.Certificate[0]) || ready.Hostname != testHostname || ready.UpstreamAddress != f.upstream.Address {
		t.Fatalf("就绪状态错误：%+v, %v", ready, err)
	}
	if _, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rotation.Ready(ctx); err == nil {
		t.Fatal("未决事务不能通过申请前检查")
	}
	state, err = f.rotation.State(ctx)
	if err != nil || state.Status != "staged" || state.CandidateHash != leafHash(f.candidate.pair.Certificate[0]) || state.PreviousHash != ready.LeafHash {
		t.Fatalf("候选事务状态错误：%+v, %v", state, err)
	}
}

func TestRotationReconcileUsesActualUpstream(t *testing.T) {
	for _, scenario := range []string{"old", "candidate", "pending-candidate", "unrelated", "unavailable", "external-include", "wrong-id"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRotationFixture(t)
			ctx := context.Background()
			id, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key)
			if err != nil {
				t.Fatal(err)
			}
			expected := "committed"
			f.upstream.active.Store(&f.candidate.pair)
			switch scenario {
			case "old":
				f.upstream.active.Store(&f.old.pair)
				expected = "rolled_back"
			case "pending-candidate":
				current := f.assertStatus(t, "staged")
				current.Status = "pending"
				root, err := os.OpenRoot(f.rotation.Directory)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				if err := saveTransaction(root, current); err != nil {
					t.Fatal(err)
				}
			case "unrelated":
				other := f.authority.issue(t, testHostname, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
				f.upstream.active.Store(&other.pair)
			case "unavailable":
				f.upstream.server.Close()
			case "external-include":
				writeTestFile(t, filepath.Join(f.rotation.Directory, includeName), []byte("# 外部修改\n"))
			case "wrong-id":
				id = "00000000000000000000000000000000"
			}
			state, err := f.rotation.Reconcile(ctx, id)
			if scenario == "unrelated" || scenario == "unavailable" || scenario == "external-include" || scenario == "wrong-id" {
				if err == nil {
					t.Fatal("无法确认的状态不应完成恢复")
				}
				f.assertStatus(t, "staged")
				return
			}
			if err != nil || state.Status != expected {
				t.Fatalf("恢复结果错误：%+v, %v", state, err)
			}
			f.assertStatus(t, expected)
			if _, err := f.rotation.Reconcile(ctx, id); err != nil {
				t.Fatal("重复恢复应返回终态", err)
			}
		})
	}
}
