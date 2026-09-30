package deployment

type FailureCode string

const (
	FailureUnavailable      FailureCode = "unavailable"
	FailureArtifact         FailureCode = "artifact_invalid"
	FailureConfiguration    FailureCode = "configuration_invalid"
	FailureConnection       FailureCode = "connection_failed"
	FailureDirectory        FailureCode = "directory_failed"
	FailureLock             FailureCode = "lock_unavailable"
	FailureUpload           FailureCode = "upload_failed"
	FailureReplace          FailureCode = "replace_failed"
	FailureValidation       FailureCode = "validation_failed"
	FailureRollback         FailureCode = "rollback_failed"
	FailureRuntimeUnknown   FailureCode = "runtime_unconfirmed"
	FailureCleanup          FailureCode = "cleanup_failed"
	FailureLockRelease      FailureCode = "lock_release_failed"
	FailureRecoveryPending  FailureCode = "recovery_pending"
	FailureRecoveryUnknown  FailureCode = "recovery_unconfirmed"
	FailureRecoveryRestored FailureCode = "recovery_restored"
)

// 只公开固定诊断，不从远端输出、错误文本或文件路径推断可展示的消息。
var publicFailureMessages = map[FailureCode]string{
	FailureRecoveryUnknown:  "本次部署未完成，已恢复旧文件，但服务或站点恢复状态未确认，已保留部署锁；请管理员核查，勿直接重试或删除锁",
	FailureRecoveryRestored: "本次部署未完成，旧文件已恢复并通过配置校验及重载，站点已恢复部署前证书；请核查失败原因后再重试",
	FailureUnavailable:      "部署服务不可用，请联系管理员检查服务配置",
	FailureArtifact:         "待部署的证书或私钥校验失败，尚未推送到服务器",
	FailureConfiguration:    "服务器部署配置无效，请检查认证、证书路径和命令配置",
	FailureConnection:       "连接部署服务器失败，请检查网络、SSH 认证及主机身份配置",
	FailureDirectory:        "无法创建证书目录，请检查部署账户的目录权限",
	FailureLock:             "无法取得部署锁，可能有任务正在执行或存在中断遗留；请管理员核查远端文件、部署锁及服务状态后再重试，勿直接删除锁",
	FailureUpload:           "证书上传失败，已尝试回滚；请检查受保护的服务日志后再重试",
	FailureReplace:          "证书文件替换失败，已尝试回滚；请检查受保护的服务日志后再重试",
	FailureValidation:       "服务配置校验失败，文件已回滚，未执行重载；请修正配置后再重试",
	FailureRollback:         "部署失败且文件回滚未完成，已保留部署锁；请管理员核查远端文件及服务状态，勿直接重试或删除锁",
	FailureRuntimeUnknown:   "服务重载失败，文件已回滚，但服务运行状态未确认；请管理员核对配置并恢复服务后再重试",
	FailureCleanup:          "证书已推送并重载，但旧文件清理未完成；请管理员核查现场，不能据此确认站点新证书已生效",
	FailureLockRelease:      "部署锁释放未完成；请管理员核查远端文件、部署锁及服务状态后再重试",
}

// PublicResult 保留结果事实，同时只允许固定错误码及中文诊断进入公开响应。
func PublicResult(result Result) Result {
	if result.Status != "failed" {
		result.FailureCode = ""
		return result
	}
	message, ok := publicFailureMessages[result.FailureCode]
	if !ok {
		result.FailureCode = ""
		message = "证书部署失败"
	}
	result.Message = message
	return result
}

// PublicStoredFailureMessage 兼容历史任务；仅原样放行完整匹配的固定诊断。
func PublicStoredFailureMessage(message string) string {
	for _, safe := range publicFailureMessages {
		if message == safe {
			return safe
		}
	}
	return "证书部署失败"
}
