package httpapi

import (
	"context"
	"errors"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// certificateTaskErrorMessage 返回可以展示给管理页面的稳定错误摘要。
func certificateTaskErrorMessage(err error) string {
	if errors.Is(err, context.Canceled) {
		return "证书任务已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "证书任务执行超时"
	}
	return "证书申请或部署失败"
}

// sanitizeDeploymentReport 移除部署失败结果中的远程路径、命令输出和底层错误。
func sanitizeDeploymentReport(report deployment.Report) deployment.Report {
	if len(report.Results) == 0 {
		return report
	}
	result := report
	result.Results = append([]deployment.Result(nil), report.Results...)
	for index := range result.Results {
		result.Results[index] = deployment.PublicResult(result.Results[index])
	}
	return result
}
