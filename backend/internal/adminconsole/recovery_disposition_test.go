package adminconsole

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type dispositionConsoleClient struct {
	recoveryTestClient
	prepares     int
	executes     int
	queries      int
	action       deployment.RecoveryAction
	executeError bool
	expired      bool
	status       string
}

func (c *dispositionConsoleClient) PrepareRecovery(id string, action deployment.RecoveryAction) (localreset.RecoveryPreparation, error) {
	c.prepares++
	c.action = action
	expires := time.Now().Add(time.Minute)
	if c.expired {
		expires = time.Now().Add(-time.Second)
	}
	return localreset.RecoveryPreparation{DispositionID: strings.Repeat("b", 64), Token: strings.Repeat("c", 64), Summary: deployment.RecoveryReviewSummary{
		TransactionID: id, ServerID: "server-1", Phase: "reload_attempted", Action: action, HistoricalEvidence: true, ObservedAt: time.Now(), ExpiresAt: expires,
	}}, nil
}
func (c *dispositionConsoleClient) ExecuteRecovery(id, token string) (deployment.RecoveryDispositionSummary, error) {
	c.executes++
	if c.executeError {
		return deployment.RecoveryDispositionSummary{}, errors.New("connection-lost")
	}
	return c.result(id), nil
}
func (c *dispositionConsoleClient) GetRecoveryDisposition(id string) (deployment.RecoveryDispositionSummary, error) {
	c.queries++
	return c.result(id), nil
}
func (c *dispositionConsoleClient) result(id string) deployment.RecoveryDispositionSummary {
	now := time.Now().UTC()
	status := c.status
	if status == "" {
		status = "succeeded"
	}
	codes := map[deployment.RecoveryAction]string{deployment.RecoveryActionRecord: "observed", deployment.RecoveryActionCommit: "committed", deployment.RecoveryActionRollback: "rolled_back"}
	code := codes[c.action]
	if status == "running" {
		code = ""
	} else if status != "succeeded" {
		code = status
	}
	return deployment.RecoveryDispositionSummary{ID: id, TransactionID: "tx-1", ServerID: "server-1", Action: c.action, Status: status, ResultCode: code, CreatedAt: now, UpdatedAt: now}
}

func TestDispositionConsoleExplicitActions(t *testing.T) {
	for _, row := range []struct {
		choice  string
		action  deployment.RecoveryAction
		message string
	}{
		{"1", deployment.RecoveryActionRecord, "核查结论已记录"},
		{"2", deployment.RecoveryActionCommit, "本次人工提交核验"},
		{"3", deployment.RecoveryActionRollback, "本次恢复基线核验"},
	} {
		t.Run(row.choice, func(t *testing.T) {
			client := &dispositionConsoleClient{}
			screen := &recoveryTestScreen{testTerminalInput: testTerminalInput{values: []string{"tx-1", row.choice, "确认 tx-1 " + dispositionActionLabel(row.action)}}}
			if err := recoveryDispositionFlow(t.Context(), client, screen); err != nil {
				t.Fatal(err)
			}
			output := strings.Join(screen.lines, "|")
			if client.action != row.action || client.prepares != 1 || client.executes != 1 || client.queries != 0 || !strings.Contains(output, row.message) {
				t.Fatal("wrong action or result")
			}
			if strings.Contains(output, strings.Repeat("c", 64)) || !strings.Contains(output, strings.Repeat("b", 64)) || !strings.Contains(output, "禁止重复执行") {
				t.Fatal("token leaked or recovery guidance absent")
			}
		})
	}
}

func TestDispositionConsoleCancellationAndUncertainResult(t *testing.T) {
	for _, mode := range []string{"quit", "cancel-action", "wrong-confirmation", "expired", "cancelled", "display-error", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &dispositionConsoleClient{}
			screen := &recoveryTestScreen{testTerminalInput: testTerminalInput{values: []string{"tx-1", "2", "确认 tx-1 完成提交"}}}
			switch mode {
			case "quit":
				screen.values[0] = "q"
			case "cancel-action":
				screen.values[1] = "q"
			case "wrong-confirmation":
				screen.values[2] = "确认 tx-1 恢复基线"
			case "expired":
				client.expired = true
			case "cancelled":
				screen.readHook = func(index int) {
					if index == 3 {
						cancel()
					}
				}
			case "display-error":
				screen.showError = true
			case "disconnect":
				client.executeError = true
			}
			_ = recoveryDispositionFlow(ctx, client, screen)
			expected := 0
			if mode == "disconnect" {
				expected = 1
			}
			if client.executes != expected || client.queries != 0 {
				t.Fatalf("unexpected retry or cancelled execution: %d", client.executes)
			}
			output := strings.Join(screen.lines, "|")
			if strings.Contains(output, "本次人工提交核验及结束审计完成") {
				t.Fatal("failure reported success")
			}
			if mode == "disconnect" && !strings.Contains(output, "不能断言未执行或执行成功") {
				t.Fatal("missing uncertainty warning")
			}
		})
	}
}

func TestDispositionConsoleQueryOnlyAndStatusWarnings(t *testing.T) {
	for _, status := range []string{"running", "uncertain", "rejected", "succeeded"} {
		client := &dispositionConsoleClient{status: status, action: deployment.RecoveryActionRecord}
		screen := &recoveryTestScreen{testTerminalInput: testTerminalInput{values: []string{strings.Repeat("b", 64)}}}
		if err := recoveryResultFlow(t.Context(), client, screen); err != nil {
			t.Fatal(err)
		}
		if client.prepares != 0 || client.executes != 0 || client.queries != 1 {
			t.Fatal("query changed state")
		}
		output := strings.Join(screen.lines, "|")
		if (status == "running" || status == "uncertain") && !strings.Contains(output, "禁止重复执行") {
			t.Fatal("missing warning")
		}
		if status == "succeeded" && !strings.Contains(output, "未改变原事务终态") {
			t.Fatal("record action falsely claims recovery")
		}
	}
}
