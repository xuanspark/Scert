package adminconsole

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type recoveryReconciliationClient interface {
	recoveryDispositionClient
	PrepareRecoveryReconciliation(string) (localreset.RecoveryPreparation, error)
}

func recoveryReconciliationFlow(ctx context.Context, client recoveryReconciliationClient, screen terminal) error {
	sourceID, err := screen.Read(ctx, "请输入结果不确定的原处置编号；本操作只重新核验，不重放文件修改或服务重载", false)
	if err != nil {
		return err
	}
	sourceID = strings.TrimSpace(sourceID)
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := client.PrepareRecoveryReconciliation(sourceID)
	if err != nil {
		_ = screen.Show("无法准备重新核查：主服务可能尚未支持，或原处置未结束、历史证据不足、现场不满足收尾条件。占用仍保留。")
		return err
	}
	defer func() { prepared.Token = "" }()
	summary := prepared.Summary
	if summary.Validate() != nil || summary.SourceDispositionID != sourceID || !time.Now().Before(summary.ExpiresAt) {
		return localreset.ErrProtocol
	}
	for _, line := range []string{
		"原处置：" + terminalValue(sourceID),
		"新核查编号：" + terminalValue(prepared.DispositionID) + "（请保留，用于断连后查询）",
		fmt.Sprintf("事务：%s；服务器：%s；动作：%s", terminalValue(summary.TransactionID), terminalValue(summary.ServerID), dispositionActionLabel(summary.Action)),
		"本次只核验原方向，不能改选提交或回滚；不写远端文件、不删锁、不重载。文件、残留、严格 HTTPS 和关闭检查全部通过后，才以新审计结束原事务并释放占用。",
		"原不确定记录永久保留；任何核验或审计失败均不释放占用，运行中处置不会因等待超时被接管。",
	} {
		if err := screen.Show(line); err != nil {
			return err
		}
	}
	expected := "确认重新核查 " + sourceID + " " + dispositionActionLabel(summary.Action)
	answer, err := screen.Read(ctx, "如确认，请完整输入："+expected+"；其他输入取消", false)
	if err != nil {
		return err
	}
	if answer != expected || ctx.Err() != nil || !time.Now().Before(summary.ExpiresAt) {
		return screen.Show("已取消或确认过期，未提交重新核查。")
	}
	result, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token)
	prepared.Token = ""
	if err != nil || result.Validate() != nil || result.ID != prepared.DispositionID || result.SourceDispositionID != sourceID ||
		result.TransactionID != summary.TransactionID || result.ServerID != summary.ServerID || result.Action != summary.Action {
		_ = screen.Show("未取得可靠结果，请重新连接后按新核查编号查询；不要重复执行或直接删除占用。")
		return localreset.ErrUnavailable
	}
	return showRecoveryDisposition(screen, result)
}
