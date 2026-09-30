package httpapi

import (
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestSanitizeDeploymentReportKeepsSafeDiagnosisWithoutMutatingEvidence(t *testing.T) {
	report := deployment.Report{Status: "failed", Results: []deployment.Result{
		{Status: "failed", FailureCode: deployment.FailureLock, Message: "secret-path"},
		{Status: "failed", FailureCode: "secret-code", Message: "secret-output"},
	}}
	public := sanitizeDeploymentReport(report)
	if public.Results[0].FailureCode != deployment.FailureLock || public.Results[0].Message != deployment.PublicResult(report.Results[0]).Message {
		t.Fatalf("固定锁诊断丢失：%+v", public)
	}
	if public.Results[1].FailureCode != "" || public.Results[1].Message != "证书部署失败" {
		t.Fatalf("未知诊断未脱敏：%+v", public)
	}
	if report.Results[0].Message != "secret-path" || report.Results[1].FailureCode != "secret-code" {
		t.Fatal("公开响应脱敏修改了受保护的原始证据")
	}
	stored := taskDeploymentResults(report)
	if stored[0].Message != public.Results[0].Message || stored[1].Message != "证书部署失败" {
		t.Fatalf("接口工作流未持久化安全诊断：%+v", stored)
	}
}
