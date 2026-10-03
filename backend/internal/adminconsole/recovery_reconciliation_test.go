package adminconsole

import (
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type reconciliationConsoleClient struct {
	dispositionConsoleClient
	sourceID string
	mismatch bool
}

func (c *reconciliationConsoleClient) PrepareRecoveryReconciliation(sourceID string) (localreset.RecoveryPreparation, error) {
	c.sourceID = sourceID
	value, err := c.PrepareRecovery("tx-1", deployment.RecoveryActionVerifyCommit)
	value.Summary.SourceDispositionID = sourceID
	value.Summary.Phase = deployment.TransactionPhaseCommitVerified
	return value, err
}
func (c *reconciliationConsoleClient) ExecuteRecovery(id, token string) (deployment.RecoveryDispositionSummary, error) {
	value, err := c.dispositionConsoleClient.ExecuteRecovery(id, token)
	if err != nil { return value, err }
	value.SourceDispositionID = c.sourceID
	if value.Status == "succeeded" { value.ResultCode = "committed" }
	if c.mismatch { value.SourceDispositionID = strings.Repeat("d", 64) }
	return value, nil
}

func TestRecoveryReconciliationConsoleConfirmation(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "cancelled", "expired", "disconnect", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			sourceID := strings.Repeat("a", 64)
			client := &reconciliationConsoleClient{}
			answer := "确认重新核查 " + sourceID + " " + dispositionActionLabel(deployment.RecoveryActionVerifyCommit)
			switch mode {
			case "rejected": client.status = "rejected"
			case "cancelled": answer = "确认"
			case "expired": client.expired = true
			case "disconnect": client.executeError = true
			case "mismatch": client.mismatch = true
			}
			screen := &recoveryTestScreen{testTerminalInput: testTerminalInput{values: []string{sourceID, answer}}}
			err := recoveryReconciliationFlow(t.Context(), client, screen)
			output := strings.Join(screen.lines, "|")
			if client.prepares != 1 || client.queries != 0 || strings.Contains(output, strings.Repeat("c", 64)) { t.Fatal("终端重复调用或泄露令牌") }
			if mode == "cancelled" || mode == "expired" {
				if client.executes != 0 { t.Fatal("取消或过期仍执行核查") }
				return
			}
			if client.executes != 1 || !strings.Contains(output, "原不确定记录永久保留") { t.Fatal("缺少明确警告或重复执行") }
			if mode == "disconnect" || mode == "mismatch" {
				if err == nil || !strings.Contains(output, "新核查编号查询") { t.Fatal("失去可靠响应后误报成功") }
			} else if err != nil { t.Fatal(err) }
			if mode == "success" && !strings.Contains(output, "占用已安全释放") { t.Fatal("缺少完成结果") }
			if mode == "rejected" && !strings.Contains(output, "原占用继续保留") { t.Fatal("错误显示拒绝结果") }
		})
	}
}
