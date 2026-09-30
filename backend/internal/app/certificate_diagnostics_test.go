package app

import (
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestTaskDeploymentResultsPersistSafeFailureDiagnostics(t *testing.T) {
	report := deployment.Report{Results: []deployment.Result{
		{Status: "failed", FailureCode: deployment.FailureLock, Message: "secret-path"},
		{Status: "failed", FailureCode: "secret-code", Message: "secret-output"},
		{Status: "success", Message: "部署完成"},
	}}
	results := taskDeploymentResults(report)
	if len(results) != 3 || results[0].Message != deployment.PublicResult(report.Results[0]).Message || results[1].Message != "证书部署失败" || results[2].Message != "部署完成" {
		t.Fatalf("持久化部署诊断不正确：%+v", results)
	}
	if report.Results[0].Message != "secret-path" {
		t.Fatal("不得覆盖受保护的原始错误")
	}
}
