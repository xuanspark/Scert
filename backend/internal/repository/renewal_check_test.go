package repository

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestRenewalCheckScopeVisibilityAndCompletion(t *testing.T) {
	repo, db := newTaskRepositoryTest(t)
	defer db.Close()
	ctx := t.Context()
	for _, role := range []string{UserRoleSuperAdmin, UserRoleAdmin, UserRoleUser} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, status) VALUES (?, ?, 'test-hash', ?, 'active')`, role, role, role); err != nil {
			t.Fatal(err)
		}
	}
	task := Task{ID: "global-check", DomainID: RenewalCheckResourceID, TaskType: "renewal_check", Status: "queued", Stage: "queued", OperatorID: UserRoleSuperAdmin}
	for _, forged := range []Task{
		{ID: "forged-domain", DomainID: "domain-1", TaskType: "renewal_check", Status: "queued", Stage: "queued"},
		{ID: "forged-management", DomainID: ManagementResourceID, TaskType: "renewal_check", Status: "queued", Stage: "queued"},
		{ID: "forged-type", DomainID: RenewalCheckResourceID, TaskType: "certificate_issue", Status: "queued", Stage: "queued"},
		{ID: "forged-project", DomainID: RenewalCheckResourceID, TaskType: "renewal_check", Status: "queued", Stage: "queued", HistoryScope: HistoryScope{ScopeType: ScopeProject, ProjectID: "default"}},
	} {
		if err := repo.CreateTask(ctx, forged); err == nil {
			t.Fatalf("接受了伪造范围：%+v", forged)
		}
	}
	if err := repo.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	duplicate := task
	duplicate.ID = "duplicate-check"
	if err := repo.CreateTask(ctx, duplicate); !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("未拒绝重复检查：%v", err)
	}
	if err := repo.CreateAttempt(ctx, TaskAttempt{ID: "check-attempt", TaskID: task.ID, AttemptNumber: 1, Status: "running", Stage: "renewal_check"}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{UserRoleAdmin, UserRoleUser} {
		if _, err := repo.GetVisibleTask(ctx, role, task.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("全局任务越权可见：%s %v", role, err)
		}
		if err := repo.RequestCancelVisibleTask(ctx, role, task.ID); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("全局任务越权取消：%s %v", role, err)
		}
	}
	if stored, err := repo.GetVisibleTask(ctx, UserRoleSuperAdmin, task.ID); err != nil || stored.ScopeType != ScopeSystem || stored.ProjectID != "" {
		t.Fatalf("系统范围丢失：%+v %v", stored, err)
	}
	completion := TaskCompletion{TaskID: task.ID, AttemptID: "check-attempt", Status: "success", Stage: "renewal_check", Message: "续期检查完成：检查 2 项，成功 1 项", FinishedAt: time.Now().UTC(), AuditAction: "renewal_check"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_renewal_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishTaskAttempt(ctx, completion); err == nil {
		t.Fatal("审计失败未回滚")
	}
	stored, err := repo.GetTask(ctx, task.ID)
	if err != nil || stored.Status != "queued" {
		t.Fatalf("审计失败仍保存任务成功：%+v %v", stored, err)
	}
	attempts, err := repo.ListAttempts(ctx, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "running" {
		t.Fatalf("审计失败仍保存尝试成功：%+v %v", attempts, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_renewal_audit`); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishTaskAttempt(ctx, completion); err != nil {
		t.Fatal(err)
	}
	var scope, resourceType, resourceID, message string
	if err := db.QueryRowContext(ctx, `SELECT scope_type, resource_type, resource_id, message FROM audit_logs WHERE task_id = ?`, task.ID).Scan(&scope, &resourceType, &resourceID, &message); err != nil {
		t.Fatal(err)
	}
	if scope != ScopeSystem || resourceType != "task" || resourceID != task.ID || message != completion.Message {
		t.Fatalf("全局检查被伪装为管理证书或未记录摘要：%s %s %s %s", scope, resourceType, resourceID, message)
	}
	for _, role := range []string{UserRoleSuperAdmin, UserRoleAdmin, UserRoleUser} {
		logs, err := NewSQLProjectMemberRepository(db).ListVisibleAuditLogs(ctx, role, 10)
		want := 0
		if role == UserRoleSuperAdmin {
			want = 1
		}
		if err != nil || len(logs) != want {
			t.Fatalf("全局续期审计可见性错误：%s %+v %v", role, logs, err)
		}
	}
	if _, _, err := repo.RetryVisibleTask(ctx, UserRoleSuperAdmin, task.ID, nil); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("全局检查不应通用重试：%v", err)
	}
}
