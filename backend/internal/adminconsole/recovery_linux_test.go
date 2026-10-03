//go:build linux

package adminconsole

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type consoleIPCSession struct {
	selected  atomic.Int32
	verified  atomic.Int32
	resets    atomic.Int32
	inspected atomic.Int32
}

func (*consoleIPCSession) Methods() []localreset.Method {
	return []localreset.Method{{ID: "email", Label: "绑定邮箱验证码"}}
}
func (s *consoleIPCSession) Select(context.Context, string) error { s.selected.Add(1); return nil }
func (s *consoleIPCSession) Verify(context.Context, string) error { s.verified.Add(1); return nil }
func (s *consoleIPCSession) Reset(context.Context, string) error  { s.resets.Add(1); return nil }
func (*consoleIPCSession) Close()                                 {}
func (*consoleIPCSession) RecoveryAvailable() bool                { return true }
func (*consoleIPCSession) ListRecoveries(context.Context, string) (deployment.PendingRecoveryPage, error) {
	return deployment.PendingRecoveryPage{Items: []deployment.PendingRecovery{{ID: "tx-1", ServerID: "server-1"}}}, nil
}
func (s *consoleIPCSession) InspectRecovery(_ context.Context, id string) (deployment.RecoveryInspection, error) {
	s.inspected.Add(1)
	return deployment.RecoveryInspection{TransactionID: id, ServerID: "server-1", ObservedAt: time.Now(),
		Files: make([]deployment.RecoveryFileObservation, 9), Locks: make([]deployment.RecoveryLockObservation, 3)}, nil
}

func TestRecoveryConsoleOverProtectedUnixSocket(t *testing.T) {
	for _, action := range []string{"inspect", "reset", "quit"} {
		t.Run(action, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sock")
			server, err := localreset.Listen(path)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			info, err := os.Lstat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("unprotected socket")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			session := &consoleIPCSession{}
			done := make(chan error, 1)
			go func() {
				done <- server.Serve(ctx, func(context.Context, localreset.Peer) (localreset.Session, error) { return session, nil })
			}()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			conn, err := localreset.Dial(ctx, path, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			screen := &recoveryTestScreen{}
			switch action {
			case "inspect":
				screen.values = []string{"2", "1"}
			case "reset":
				screen.values = []string{"1", "1", "01234567", "NewPass123", "NewPass123"}
			case "quit":
				screen.values = []string{"q"}
			}
			if err := runConnected(ctx, conn, screen); err != nil {
				t.Fatal(err)
			}
			if action == "reset" {
				if session.resets.Load() != 1 || session.selected.Load() != 1 || session.verified.Load() != 1 || session.inspected.Load() != 0 {
					t.Fatal("reset verification flow changed")
				}
			} else {
				if session.resets.Load() != 0 || session.selected.Load() != 0 || session.verified.Load() != 0 {
					t.Fatal("non-reset menu invoked reset")
				}
			}
			if action == "inspect" && (session.inspected.Load() != 1 || !strings.Contains(strings.Join(screen.lines, "|"), "只读采样已完成")) {
				t.Fatal("inspection did not reach console")
			}
		})
	}
}
