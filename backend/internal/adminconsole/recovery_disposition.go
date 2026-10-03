package adminconsole

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type recoveryDispositionClient interface {
	InspectRecovery(string) (deployment.RecoveryInspection, error)
	PrepareRecovery(string, deployment.RecoveryAction) (localreset.RecoveryPreparation, error)
	ExecuteRecovery(string, string) (deployment.RecoveryDispositionSummary, error)
	GetRecoveryDisposition(string) (deployment.RecoveryDispositionSummary, error)
}

func recoveryDispositionFlow(ctx context.Context, client recoveryDispositionClient, screen terminal) error {
	id, err := screen.Read(ctx, "请输入待处置事务编号（可先使用只读核查获取）；输入 q 退出", false)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "q" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	inspection, err := client.InspectRecovery(id)
	if err != nil {
		_ = screen.Show("无法核查事务，未提交人工处置请求。请重新启动本机管理。")
		return err
	}
	if err := showRecovery(screen, inspection); err != nil {
		return err
	}
	choice, err := screen.Read(ctx, "选择明确动作：1 仅记录核查；2 完成提交（保留新证书并清理备份）；3 恢复基线（恢复旧证书并重载）；其他输入取消", false)
	if err != nil {
		return err
	}
	actions := map[string]deployment.RecoveryAction{
		"1": deployment.RecoveryActionRecord, "2": deployment.RecoveryActionCommit, "3": deployment.RecoveryActionRollback,
	}
	action, ok := actions[strings.TrimSpace(choice)]
	if !ok {
		return screen.Show("已取消，未提交人工处置请求。")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := client.PrepareRecovery(id, action)
	if err != nil {
		_ = screen.Show("准备失败或历史证据不足，未提交执行请求。请重新核查，不要手动删锁。")
		return err
	}
	defer func() { prepared.Token = "" }()
	if prepared.Summary.Validate() != nil || prepared.Summary.TransactionID != id || prepared.Summary.Action != action || !time.Now().Before(prepared.Summary.ExpiresAt) {
		return localreset.ErrProtocol
	}
	lines := []string{
		fmt.Sprintf("处置编号：%s（请保留，用于断连后查询结果）", terminalValue(prepared.DispositionID)),
		fmt.Sprintf("事务：%s；服务器：%s；阶段：%s；动作：%s", terminalValue(id), terminalValue(prepared.Summary.ServerID), recoveryLabel(prepared.Summary.Phase), dispositionActionLabel(action)),
		"确认截止时间：" + prepared.Summary.ExpiresAt.Local().Format("2006-01-02 15:04:05 MST"),
		"仅记录只保存核查审计，不结束原事务；提交或回滚会修改远端文件，可能清理备份或重载服务。动作失败不会自动切换方向。",
		"断连或结果不确定时，禁止重复执行；重新启动本机管理，选择查询人工处置结果。",
	}
	for _, line := range lines {
		if err := screen.Show(line); err != nil {
			return err
		}
	}
	expected := "确认 " + id + " " + dispositionActionLabel(action)
	confirmation, err := screen.Read(ctx, "如确认执行，请完整输入："+expected+"；其他输入取消", false)
	if err != nil {
		return err
	}
	if confirmation != expected {
		return screen.Show("已取消，未发送执行请求。")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(prepared.Summary.ExpiresAt) {
		return screen.Show("确认已过期，未发送执行请求；请重新核查。")
	}
	result, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token)
	prepared.Token = ""
	if err != nil {
		_ = screen.Show("未取得可靠结果，不能断言未执行或执行成功。请重新连接，按处置编号查询；不要重复提交或删除远端锁。")
		return err
	}
	if result.ID != prepared.DispositionID || result.TransactionID != id || result.ServerID != prepared.Summary.ServerID || result.Action != action {
		_ = screen.Show("响应与确认内容不一致，不能认定成功；请按处置编号查询。")
		return localreset.ErrProtocol
	}
	return showRecoveryDisposition(screen, result)
}

func recoveryResultFlow(ctx context.Context, client recoveryDispositionClient, screen terminal) error {
	id, err := screen.Read(ctx, "请输入人工处置编号（不是事务编号）；本操作只查询，不重放、不释放占用", false)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	result, err := client.GetRecoveryDisposition(id)
	if err != nil || result.ID != id {
		_ = screen.Show("未取得有效处置结果；记录不存在或通道不可用不代表未执行，请保留编号继续核查。")
		return localreset.ErrUnavailable
	}
	return showRecoveryDisposition(screen, result)
}

func showRecoveryDisposition(screen terminal, result deployment.RecoveryDispositionSummary) error {
	if result.Validate() != nil {
		return localreset.ErrProtocol
	}
	message := "处置仍在运行或结束审计尚未完成；保留占用，禁止重复执行。"
	switch result.Status {
	case "succeeded":
		switch result.Action {
		case deployment.RecoveryActionRecord:
			message = "核查结论已记录；未改变原事务终态，不代表历史部署成功。"
		case deployment.RecoveryActionCommit:
			message = "本次人工提交核验及结束审计完成；不代表项目全部生产验收通过。"
		case deployment.RecoveryActionRollback:
			message = "本次恢复基线核验及结束审计完成；不代表项目全部生产验收通过。"
		}
	case "rejected":
		message = "处置已拒绝，未执行本次远端变更；请重新核查拒绝原因。"
	case "uncertain":
		message = "处置结果不确定，可能已修改远端；占用继续保留，禁止重复执行或手动删锁。"
	}
	if err := screen.Show(fmt.Sprintf("处置：%s；事务：%s；服务器：%s；动作：%s", terminalValue(result.ID), terminalValue(result.TransactionID), terminalValue(result.ServerID), dispositionActionLabel(result.Action))); err != nil {
		return err
	}
	return screen.Show(message)
}

func dispositionActionLabel(action deployment.RecoveryAction) string {
	switch action {
	case deployment.RecoveryActionRecord:
		return "仅记录核查"
	case deployment.RecoveryActionCommit:
		return "完成提交"
	case deployment.RecoveryActionRollback:
		return "恢复基线"
	}
	return "未知动作"
}
