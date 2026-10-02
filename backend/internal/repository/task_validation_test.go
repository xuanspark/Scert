package repository

import (
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
)

func TestTaskValidationPersistsAndEnforcesVisibility(t *testing.T) {
	repo, db := newTaskRepositoryTest(t)
	defer db.Close()
	completion := seedTaskCompletion(t, repo)
	if _, err := db.ExecContext(t.Context(), `UPDATE tasks SET task_type = 'certificate_validate' WHERE id = ?`, completion.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users (id, username, password_hash, role, status)
		VALUES ('reader', 'reader', 'test-only', 'user', 'active'), ('outsider', 'outsider', 'test-only', 'user', 'active')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO project_members (project_id, user_id, role) VALUES ('default', 'reader', 'read_only')`); err != nil {
		t.Fatal(err)
	}
	completion.Stage = "certificate_validate"
	completion.AuditAction = "certificate_validate"
	completion.Validation = &certificate.ValidationResult{
		Domains: []string{"example.test", "*.example.test"}, SerialNumber: "1234",
		FingerprintSHA256: "test-fingerprint", NotBefore: time.Now().UTC().Truncate(time.Second),
		NotAfter: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second), ChainLength: 2,
	}
	if err := repo.FinishTaskAttempt(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	reader := NewSQLTaskRepository(db)
	items, err := reader.ListVisibleTaskValidations(t.Context(), "reader", completion.TaskID)
	if err != nil || len(items) != 1 || items[0].AttemptID != completion.AttemptID || items[0].AttemptNumber != 1 {
		t.Fatalf("重建仓储后校验结果丢失：%+v %v", items, err)
	}
	got := items[0].Validation
	if len(got.Domains) != 2 || got.SerialNumber != "1234" || got.FingerprintSHA256 != "test-fingerprint" || got.ChainLength != 2 || !got.NotAfter.Equal(completion.Validation.NotAfter) {
		t.Fatalf("校验详情不完整：%+v", got)
	}
	for _, actor := range []string{"outsider", "", "missing"} {
		items, err := reader.ListVisibleTaskValidations(t.Context(), actor, completion.TaskID)
		if err != nil || len(items) != 0 {
			t.Fatalf("无权限账户读取校验结果：%s %+v %v", actor, items, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM project_members WHERE user_id = 'reader'`); err != nil {
		t.Fatal(err)
	}
	items, err = reader.ListVisibleTaskValidations(t.Context(), "reader", completion.TaskID)
	if err != nil || len(items) != 0 {
		t.Fatalf("撤权后仍可读取校验结果：%+v %v", items, err)
	}
	reports, err := repo.ListTaskDeploymentReports(t.Context(), completion.TaskID)
	if err != nil || len(reports) != 0 {
		t.Fatalf("校验结果被伪装成部署报告：%+v %v", reports, err)
	}
}

func TestTaskValidationFailureRollsBackAllRecords(t *testing.T) {
	for _, scenario := range []string{"result_insert", "audit_insert", "wrong_task_type", "mixed_report", "failed_status", "foreign_attempt"} {
		t.Run(scenario, func(t *testing.T) {
			repo, db := newTaskRepositoryTest(t)
			defer db.Close()
			completion := seedTaskCompletion(t, repo)
			completion.Validation = &certificate.ValidationResult{Domains: []string{"example.test"}}
			completion.AuditAction = "certificate_validate"
			if scenario != "wrong_task_type" {
				if _, err := db.ExecContext(t.Context(), `UPDATE tasks SET task_type = 'certificate_validate' WHERE id = ?`, completion.TaskID); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "result_insert", "audit_insert":
				table := "task_validation_results"
				if scenario == "audit_insert" {
					table = "audit_logs"
				}
				if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_validation BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "mixed_report":
				completion.Report = completionTestReport()
			case "failed_status":
				completion.Status = "failed"
			case "foreign_attempt":
				completion.AttemptID = "missing-attempt"
			}
			if err := repo.FinishTaskAttempt(t.Context(), completion); err == nil {
				t.Fatal("无效校验结果或写入失败必须回滚终态")
			}
			task, err := repo.GetTask(t.Context(), completion.TaskID)
			if err != nil || task.Status != "running" || task.FinishedAt != nil {
				t.Fatalf("失败后任务被部分提交：%+v %v", task, err)
			}
			attempts, err := repo.ListAttempts(t.Context(), completion.TaskID)
			if err != nil || len(attempts) != 1 || attempts[0].Status != "running" || attempts[0].FinishedAt != nil {
				t.Fatalf("失败后尝试被部分提交：%+v %v", attempts, err)
			}
			for _, table := range []string{"task_validation_results", "task_deployment_reports", "task_deployment_results", "audit_logs"} {
				var count int
				if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE task_id = ?`, completion.TaskID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("失败后出现部分记录：%s %d %v", table, count, err)
				}
			}
		})
	}
}
