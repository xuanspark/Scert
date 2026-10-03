//go:build linux

package adminconsole

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type dispositionConsoleSession struct {
	consoleIPCSession
	actions dispositionConsoleClient
}

func (*dispositionConsoleSession) DispositionAvailable() bool { return true }
func (s *dispositionConsoleSession) PrepareRecovery(_ context.Context, id string, action deployment.RecoveryAction) (localreset.RecoveryPreparation, error) {
	return s.actions.PrepareRecovery(id, action)
}
func (s *dispositionConsoleSession) ExecuteRecovery(_ context.Context, id, token string) (deployment.RecoveryDispositionSummary, error) {
	return s.actions.ExecuteRecovery(id, token)
}
func (s *dispositionConsoleSession) GetRecoveryDisposition(_ context.Context, id string) (deployment.RecoveryDispositionSummary, error) {
	return s.actions.GetRecoveryDisposition(id)
}

func TestDispositionConsoleUnixMenus(t *testing.T) {
	for _, mode := range []string{"record", "commit", "rollback", "query", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sock")
			server, err := localreset.Listen(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			session := &dispositionConsoleSession{}
			done := make(chan error, 1)
			screen := &recoveryTestScreen{}
			switch mode {
			case "record":
				screen.values = []string{"3", "tx-1", "1", "确认 tx-1 仅记录核查"}
			case "commit":
				screen.values = []string{"3", "tx-1", "2", "确认 tx-1 完成提交"}
			case "rollback":
				screen.values = []string{"3", "tx-1", "3", "确认 tx-1 恢复基线"}
			case "query":
				screen.values = []string{"4", strings.Repeat("b", 64)}
				session.actions.action = deployment.RecoveryActionRecord
			case "cancel":
				screen.values = []string{"3", "tx-1", "2", "取消"}
			}
			go func() {
				done <- server.Serve(ctx, func(context.Context, localreset.Peer) (localreset.Session, error) { return session, nil })
			}()
			defer server.Close()
			defer cancel()
			conn, err := localreset.Dial(ctx, path, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			err = runConnected(ctx, conn, screen)
			cancel()
			if serveErr := <-done; serveErr != nil {
				t.Fatal(serveErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			if session.resets.Load() != 0 || session.selected.Load() != 0 || session.verified.Load() != 0 {
				t.Fatal("recovery menu invoked password reset")
			}
			if mode == "query" {
				if session.actions.queries != 1 || session.actions.prepares != 0 || session.actions.executes != 0 {
					t.Fatal("query replayed")
				}
			} else {
				expected := 1
				if mode == "cancel" {
					expected = 0
				}
				if session.actions.prepares != 1 || session.actions.executes != expected || session.actions.queries != 0 {
					t.Fatal("wrong menu dispatch")
				}
			}
			if strings.Contains(strings.Join(screen.lines, "|"), strings.Repeat("c", 64)) {
				t.Fatal("token printed")
			}
		})
	}
}
