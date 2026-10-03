package adminconsole

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type recoveryClient interface {
	ListRecoveries(string) (deployment.PendingRecoveryPage, error)
	InspectRecovery(string) (deployment.RecoveryInspection, error)
}

func recoveryFlow(ctx context.Context, client recoveryClient, screen terminal) error {
	if err := screen.Show("只读核查：不写入文件、不执行远端命令、不删锁、不改变事务状态。结果是当前采样，不证明历史部署成功。每页显示 5 条。"); err != nil {
		return err
	}
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := client.ListRecoveries(cursor)
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			return screen.Show("当前没有更多待核查的部署事务。")
		}
		for index, item := range page.Items {
			line := fmt.Sprintf("%d. 事务：%s；服务器：%s；阶段：%s；状态：%s；历史证据：%s", index+1,
				terminalValue(item.ID), terminalValue(item.ServerID), recoveryLabel(item.Phase), recoveryLabel(item.Status), evidenceLabel(item.HistoricalEvidence))
			if err := screen.Show(line); err != nil {
				return err
			}
		}
		choice, err := screen.Read(ctx, "输入本页序号进行只读核查；输入 n 查看下一页；输入 q 退出", false)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch strings.TrimSpace(choice) {
		case "q":
			return nil
		case "n":
			if page.NextCursor == "" {
				return screen.Show("已到最后一页；本次未执行任何写入操作。")
			}
			cursor = page.NextCursor
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(choice))
		if err != nil || index < 1 || index > len(page.Items) {
			return localreset.ErrProtocol
		}
		result, err := client.InspectRecovery(page.Items[index-1].ID)
		if err != nil {
			return err
		}
		return showRecovery(screen, result)
	}
}

func showRecovery(screen terminal, result deployment.RecoveryInspection) error {
	lines := []string{
		fmt.Sprintf("事务：%s；服务器：%s；采样时间：%s", terminalValue(result.TransactionID), terminalValue(result.ServerID), result.ObservedAt.UTC().Format("2006-01-02 15:04:05 UTC")),
		"历史证据：" + evidenceLabel(result.HistoricalEvidence),
	}
	for _, file := range result.Files {
		exists := "不存在"
		if file.Exists {
			exists = "存在"
		}
		lines = append(lines, fmt.Sprintf("%s：%s；%s；比较：%s；摘要：%s", recoveryLabel(file.Kind), terminalValue(file.Path), exists, recoveryLabel(file.Comparison), terminalValue(file.Digest)))
	}
	for _, lock := range result.Locks {
		exists := "不存在"
		if lock.Exists {
			exists = "存在"
		}
		lines = append(lines, fmt.Sprintf("锁：%s；%s；归属：%s", terminalValue(lock.Path), exists, recoveryLabel(lock.Ownership)))
	}
	lines = append(lines, "只读采样已完成，未提交或回滚事务；缺少历史证据不能视为历史部署成功。")
	for _, line := range lines {
		if err := screen.Show(line); err != nil {
			return err
		}
	}
	return nil
}

func evidenceLabel(present bool) string {
	if present {
		return "记录已保存（不代表本次部署通过验收）"
	}
	return "缺失，禁止据此认定历史成功"
}

func terminalValue(value string) string {
	var output strings.Builder
	for _, c := range value {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) || c == '\u2028' || c == '\u2029' {
			output.WriteString(fmt.Sprintf("\\u%04x", c))
		} else {
			output.WriteRune(c)
		}
	}
	return output.String()
}

func recoveryLabel(value string) string {
	labels := map[string]string{
		"unrecorded": "无历史摘要", "unproven": "归属无法证明",
		"pending": "待处理", "recovery_required": "需要恢复", "locked": "已锁定", "acquiring": "取得锁中",
		"uploaded": "已上传", "replacing": "替换中", "installed": "已安装", "reload_attempted": "已尝试重载",
		"committing": "提交中", "commit_verified": "提交已核验", "rollback_verified": "回滚已核验",
		"committed": "已提交", "rolled_back": "已回滚", "target": "目标文件", "temporary": "临时文件", "backup": "备份文件",
		"match": "匹配", "mismatch": "不匹配", "missing": "缺失", "unknown": "未知", "unverifiable": "无法验证",
		"owned": "属于本事务", "foreign": "属于其他事务", "ownerless": "缺少归属", "absent": "不存在",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return "未识别（" + terminalValue(value) + "）"
}
