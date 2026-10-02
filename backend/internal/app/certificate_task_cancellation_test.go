package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestCertificateCancellationPreservesDeploymentOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		report   deployment.Report
		expected string
	}{
		{"partial", deployment.Report{Status: "partial", Total: 2, Success: 1, Failed: 1}, "partial_success"},
		{"recovery_unconfirmed", deployment.Report{Status: "failed", RecoveryStatus: "unconfirmed", Total: 1, Failed: 1}, "failed"},
		{"recovery_failed", deployment.Report{Status: "failed", RecoveryStatus: "failed", Total: 1, Failed: 1}, "failed"},
		{"restored", deployment.Report{Status: "failed", RecoveryStatus: "success", Total: 1, Failed: 1}, "cancelled"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			w, a := newCertificateTaskFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scenario.report.Results = []deployment.Result{{ServerID: "cancel-target", Status: "failed", Message: "secret-output"}}
			if scenario.report.Success > 0 {
				scenario.report.Results = append(scenario.report.Results, deployment.Result{ServerID: "completed-target", Status: "success"})
			}
			calls := 0
			_, err := w.run(ctx, "task-domain", "renewal_run", "certificate_issue", 3, time.Nanosecond,
				func(context.Context, string) (CertificateWorkflowResult, error) {
					calls++
					cancel()
					return CertificateWorkflowResult{Deployment: scenario.report}, context.Canceled
				})
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("取消后仍重试或丢失原始取消原因：%d %v", calls, err)
			}
			task := assertCertificateTask(t, a, scenario.expected, scenario.expected)
			results, err := a.Tasks.ListTaskDeploymentResults(t.Context(), task.ID)
			if err != nil || len(results) != len(scenario.report.Results) {
				t.Fatalf("取消后必须保留实际部署结果：%+v %v", results, err)
			}
			for _, result := range results {
				if strings.Contains(result.Message, "secret-output") {
					t.Fatal("取消后的部署结果泄露原始远端输出")
				}
			}
		})
	}
}
