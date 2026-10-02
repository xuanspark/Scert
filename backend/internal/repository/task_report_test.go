package repository

import (
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func completionTestReport() *deployment.Report {
	return &deployment.Report{
		Total: 2, Success: 1, Failed: 1, Status: "failed", VerificationStatus: "failed", RecoveryStatus: "unconfirmed",
		Results: []deployment.Result{
			{ServerID: "server-1", Server: "第一台", Status: "success", BackupID: "backup-1"},
			{ServerID: "server-2", Server: "第二台", Status: "failed", FailureCode: deployment.FailureRecoveryUnknown, Message: "secret-output-marker"},
		},
	}
}

func TestTaskCompletionReportPersistsAcrossRepositoryRestart(t *testing.T) {
	repo, db := newTaskRepositoryTest(t)
	defer db.Close()
	completion := seedTaskCompletion(t, repo)
	completion.Status = "failed"
	completion.Report = completionTestReport()
	completion.AuditAction = "certificate_deploy"
	if err := repo.FinishTaskAttempt(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	reader := NewSQLTaskRepository(db)
	var auditStatus, scope, projectID, resourceID string
	if err := db.QueryRowContext(t.Context(), `SELECT status, scope_type, project_id, resource_id FROM audit_logs WHERE task_id = ?`,
		completion.TaskID).Scan(&auditStatus, &scope, &projectID, &resourceID); err != nil || auditStatus != "error" || scope != "project" || projectID != "default" || resourceID != "domain-1" {
		t.Fatalf("完成审计与任务状态或归属不一致：%s %s %s %s %v", auditStatus, scope, projectID, resourceID, err)
	}
	reports, err := reader.ListTaskDeploymentReports(t.Context(), completion.TaskID)
	if err != nil || len(reports) != 1 {
		t.Fatalf("完整报告未保存：%+v %v", reports, err)
	}
	stored := reports[0]
	if stored.AttemptNumber != 1 || stored.AttemptID != completion.AttemptID || stored.Report.VerificationStatus != "failed" || stored.Report.RecoveryStatus != "unconfirmed" || len(stored.Report.Results) != 2 {
		t.Fatalf("重读后验证或恢复证据丢失：%+v", stored)
	}
	if stored.Report.Results[1].FailureCode != deployment.FailureRecoveryUnknown || strings.Contains(stored.Report.Results[1].Message, "secret-output-marker") {
		t.Fatal("报告未保留固定诊断或泄露远端输出")
	}
	var raw string
	if err := db.QueryRowContext(t.Context(), `SELECT report_json FROM task_deployment_reports WHERE task_id = ?`, completion.TaskID).Scan(&raw); err != nil || strings.Contains(raw, "secret-output-marker") {
		t.Fatalf("持久化前未脱敏：%v", err)
	}
	results, err := reader.ListTaskDeploymentResults(t.Context(), completion.TaskID)
	if err != nil || len(results) != 2 || results[1].Message != stored.Report.Results[1].Message {
		t.Fatalf("逐目标结果与报告不一致：%+v %v", results, err)
	}
	if completion.Report.Results[1].Message != "secret-output-marker" {
		t.Fatal("保存报告不能原地修改调用者结果")
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users (id, username, password_hash, role, status)
		VALUES ('report-admin', 'report-admin', 'test', 'admin', 'active')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO project_members (project_id, user_id, role)
		VALUES ('default', 'report-admin', 'project_admin')`); err != nil {
		t.Fatal(err)
	}
	_, retry, err := repo.RetryVisibleTask(t.Context(), "report-admin", completion.TaskID, nil)
	if err != nil {
		t.Fatal(err)
	}
	completion.AttemptID = retry.ID
	completion.Status = "success"
	completion.FinishedAt = time.Now().UTC()
	completion.Report = &deployment.Report{Status: "success", VerificationStatus: "success"}
	if err := repo.FinishTaskAttempt(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	reports, err = reader.ListTaskDeploymentReports(t.Context(), completion.TaskID)
	if err != nil || len(reports) != 2 || reports[0].Report.RecoveryStatus != "unconfirmed" || reports[1].AttemptNumber != 2 || reports[1].Report.VerificationStatus != "success" {
		t.Fatalf("重试覆盖或混用了旧报告：%+v %v", reports, err)
	}
	if reports[1].Report.Results == nil {
		t.Fatal("零目标报告必须返回空数组而非空值，避免前端忽略验证状态")
	}
}

func TestTaskCompletionReportFailureRollsBackAllRecords(t *testing.T) {
	for _, table := range []string{"task_deployment_reports", "task_deployment_results", "audit_logs"} {
		t.Run(table, func(t *testing.T) {
			repo, db := newTaskRepositoryTest(t)
			defer db.Close()
			completion := seedTaskCompletion(t, repo)
			completion.Report = completionTestReport()
			completion.AuditAction = "certificate_deploy"
			if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_report BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT, 'injected report failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err := repo.FinishTaskAttempt(t.Context(), completion); err == nil {
				t.Fatal("报告保存失败必须拒绝提交终态")
			}
			task, err := repo.GetTask(t.Context(), completion.TaskID)
			if err != nil || task.Status != "running" || task.FinishedAt != nil {
				t.Fatalf("报告失败但任务被提交：%+v %v", task, err)
			}
			attempts, err := repo.ListAttempts(t.Context(), completion.TaskID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != "running" || attempts[0].FinishedAt != nil {
				t.Fatalf("报告失败但尝试被提交：%+v %v", attempts, err)
			}
			reports, err := repo.ListTaskDeploymentReports(t.Context(), completion.TaskID)
			if err != nil || len(reports) != 0 {
				t.Fatalf("出现部分提交的报告：%+v %v", reports, err)
			}
			results, err := repo.ListTaskDeploymentResults(t.Context(), completion.TaskID)
			if err != nil || len(results) != 0 {
				t.Fatalf("出现部分提交的目标结果：%+v %v", results, err)
			}
			var auditCount int
			if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_logs WHERE task_id = ?`, completion.TaskID).Scan(&auditCount); err != nil || auditCount != 0 {
				t.Fatalf("出现部分提交的完成审计：%d %v", auditCount, err)
			}
		})
	}
}
