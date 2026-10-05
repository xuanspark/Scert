package notification

import (
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// 恢复警告不受明细开关或摘要条数限制，不包含服务器名称及远端输出。
func recoveryWarning(event Event) string {
	const warning = "需要人工核查：部署恢复尚未确认或部署锁需要处理，勿直接重试或删除锁。请管理员核查服务器文件、服务和站点状态。"
	switch strings.ToLower(strings.TrimSpace(event.Deployment.RecoveryStatus)) {
	case "unconfirmed", "failed", "recovery_required":
		return warning
	}
	for _, result := range event.Deployment.Results {
		switch result.FailureCode {
		case deployment.FailureRollback, deployment.FailureRuntimeUnknown,
			deployment.FailureRecoveryPending, deployment.FailureRecoveryUnknown,
			deployment.FailureLock, deployment.FailureLockRelease:
			return warning
		}
		if hasRecoveryWarning(result.Message) {
			return warning
		}
	}
	if hasRecoveryWarning(event.Error) {
		return warning
	}
	return ""
}

// 兼容未携带结构化诊断的历史任务。
func hasRecoveryWarning(message string) bool {
	return strings.Contains(message, "部署锁") ||
		(strings.Contains(message, "恢复") &&
			(strings.Contains(message, "未确认") || strings.Contains(message, "待核查")))
}
