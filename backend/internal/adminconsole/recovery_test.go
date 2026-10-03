package adminconsole

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type recoveryTestClient struct {
	cursors   []string
	inspected []string
	err       error
}

func (c *recoveryTestClient) ListRecoveries(cursor string) (deployment.PendingRecoveryPage, error) {
	c.cursors = append(c.cursors, cursor)
	if cursor == "" {
		return deployment.PendingRecoveryPage{Items: []deployment.PendingRecovery{{ID: "tx-1", ServerID: "server-1"}}, NextCursor: "tx-1"}, nil
	}
	return deployment.PendingRecoveryPage{Items: []deployment.PendingRecovery{{ID: "tx-2", ServerID: "server-2"}}}, nil
}

func (c *recoveryTestClient) InspectRecovery(id string) (deployment.RecoveryInspection, error) {
	c.inspected = append(c.inspected, id)
	return deployment.RecoveryInspection{TransactionID: id, ServerID: "server-2", ObservedAt: time.Now(),
		Files: []deployment.RecoveryFileObservation{{Path: "/ssl/\x1b[2J\n\u202efile", Kind: "target", Comparison: "unrecorded"}}}, c.err
}

type recoveryTestScreen struct {
	testTerminalInput
	lines     []string
	showError bool
}

func (s *recoveryTestScreen) Show(line string) error {
	if s.showError {
		return errors.New("display-unavailable")
	}
	s.lines = append(s.lines, line)
	return nil
}

func TestRecoveryConsolePaginationAndSafeDisplay(t *testing.T) {
	client := &recoveryTestClient{}
	screen := &recoveryTestScreen{testTerminalInput: testTerminalInput{values: []string{"n", "1"}}}
	if err := recoveryFlow(t.Context(), client, screen); err != nil {
		t.Fatal(err)
	}
	if len(client.cursors) != 2 || client.cursors[1] != "tx-1" || len(client.inspected) != 1 || client.inspected[0] != "tx-2" {
		t.Fatal("pagination or selection incorrect")
	}
	output := strings.Join(screen.lines, "|")
	if strings.ContainsAny(output, "\x1b\n\u202e") || !strings.Contains(output, "\\u001b") ||
		!strings.Contains(output, "缺失，禁止据此认定历史成功") || !strings.Contains(output, "未提交或回滚事务") {
		t.Fatal("unsafe display or missing evidence warning")
	}
}

func TestRecoveryConsoleCancellationAndFailures(t *testing.T) {
	for _, scenario := range []string{"quit", "invalid", "cancelled", "display-error", "inspection-error"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &recoveryTestClient{}
			screen := &recoveryTestScreen{testTerminalInput: testTerminalInput{values: []string{"1"}}}
			switch scenario {
			case "quit":
				screen.values[0] = "q"
			case "invalid":
				screen.values[0] = "0"
			case "cancelled":
				screen.readHook = func(int) { cancel() }
			case "display-error":
				screen.showError = true
			case "inspection-error":
				client.err = errors.New("unavailable")
			}
			err := recoveryFlow(ctx, client, screen)
			if (err == nil) != (scenario == "quit") {
				t.Fatalf("unexpected result: %v", err)
			}
			if scenario != "inspection-error" && len(client.inspected) != 0 {
				t.Fatal("cancelled operation inspected")
			}
			if strings.Contains(strings.Join(screen.lines, "|"), "只读采样已完成") {
				t.Fatal("failure reported success")
			}
		})
	}
}
