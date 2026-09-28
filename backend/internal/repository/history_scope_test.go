package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestHistoryScopeSQLite(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "history.db")
	checkHistoryScope(t, cfg)
}

func TestHistoryScopeMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	checkHistoryScope(t, server.Database(t))
}

func checkHistoryScope(t *testing.T, cfg config.DatabaseConfig) {
	t.Helper()
	ctx := t.Context()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(ctx, db.DB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	projects := NewSQLProjectRepository(db.DB, cfg.Driver)
	domains := NewSQLDomainRepository(db.DB, cfg.Driver)
	tasks := NewSQLTaskRepository(db.DB, cfg.Driver)
	audit := NewSQLAuditRepository(db.DB, cfg.Driver)
	for _, id := range []string{"history-a", "history-b"} {
		if err := projects.CreateProject(ctx, domain.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	resource, err := domain.NewDomain("history.example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	resource.ProjectID = "history-a"
	if err := domains.Create(ctx, resource); err != nil {
		t.Fatal(err)
	}
	expected := HistoryScope{ScopeType: ScopeProject, ProjectID: "history-a"}
	task := Task{ID: "history-task", DomainID: resource.ID, TaskType: "certificate_issue", Status: "running", Stage: "issue", OperatorID: "actor", IdempotencyKey: "history-key"}
	if err := tasks.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() (Task, error){
		"get":         func() (Task, error) { return tasks.GetTask(ctx, task.ID) },
		"active":      func() (Task, error) { return tasks.FindActiveTaskByDomain(ctx, resource.ID, task.TaskType) },
		"idempotency": func() (Task, error) { return tasks.FindTaskByIdempotencyKey(ctx, resource.ID, task.IdempotencyKey) },
	} {
		t.Run(name, func(t *testing.T) {
			stored, err := read()
			if err != nil || stored.HistoryScope != expected || stored.OperatorID != "actor" {
				t.Fatalf("任务范围读取错误：%+v，%v", stored, err)
			}
		})
	}
	for _, invalid := range []Task{
		{ID: "missing", DomainID: "missing", TaskType: "certificate_issue"},
		{ID: "forged-project", DomainID: resource.ID, TaskType: "certificate_issue", HistoryScope: HistoryScope{ProjectID: "history-b"}},
		{ID: "forged-system", DomainID: resource.ID, TaskType: "certificate_issue", HistoryScope: HistoryScope{ScopeType: ScopeSystem}},
		{ID: "forged-unknown", DomainID: resource.ID, TaskType: "certificate_issue", HistoryScope: HistoryScope{ScopeType: ScopeUnknown}},
		{ID: "false-management", DomainID: resource.ID, TaskType: "management_renewal"},
		{ID: "false-resource", DomainID: ManagementResourceID, TaskType: "certificate_issue"},
		{ID: "system-project", DomainID: ManagementResourceID, TaskType: "management_renewal", HistoryScope: expected},
	} {
		t.Run(invalid.ID, func(t *testing.T) {
			invalid.Status, invalid.Stage = "queued", "queued"
			if err := tasks.CreateTask(ctx, invalid); err == nil {
				t.Fatal("无效任务范围未拒绝")
			}
		})
	}
	attempt := TaskAttempt{ID: "history-attempt", TaskID: task.ID, AttemptNumber: 1, Stage: "issue", Status: "running"}
	if err := tasks.CreateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []TaskAttempt{
		{ID: "missing-parent", TaskID: "missing"},
		{ID: "wrong-project", TaskID: task.ID, HistoryScope: HistoryScope{ProjectID: "history-b"}},
		{ID: "wrong-system", TaskID: task.ID, HistoryScope: HistoryScope{ScopeType: ScopeSystem}},
	} {
		t.Run(invalid.ID, func(t *testing.T) {
			invalid.AttemptNumber, invalid.Stage, invalid.Status = 2, "issue", "running"
			if err := tasks.CreateAttempt(ctx, invalid); err == nil {
				t.Fatal("无效任务尝试关联未拒绝")
			}
		})
	}
	management := Task{ID: "management", DomainID: ManagementResourceID, TaskType: "management_renewal", Status: "running", Stage: "issue"}
	if err := tasks.CreateTask(ctx, management); err != nil {
		t.Fatal(err)
	}
	if stored, err := tasks.GetTask(ctx, management.ID); err != nil || stored.HistoryScope != (HistoryScope{ScopeType: ScopeSystem}) {
		t.Fatalf("系统任务范围错误：%+v，%v", stored, err)
	}
	if err := tasks.CreateAttempt(ctx, TaskAttempt{ID: "management-attempt", TaskID: management.ID, AttemptNumber: 1, Stage: "issue", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if attempts, err := tasks.ListAttempts(ctx, management.ID); err != nil || len(attempts) != 1 || attempts[0].HistoryScope != (HistoryScope{ScopeType: ScopeSystem}) {
		t.Fatalf("系统尝试范围错误：%+v，%v", attempts, err)
	}
	for _, entry := range []AuditLog{
		{Action: "direct", ResourceType: "domain", ResourceID: resource.ID},
		{Action: "linked", ResourceType: "domain", ResourceID: resource.ID, TaskID: task.ID, OperatorID: "actor"},
		{Action: "task", ResourceType: "task", ResourceID: task.ID},
		{Action: "system", ResourceType: "management_certificate", ResourceID: ManagementResourceID, TaskID: management.ID},
		{Action: "auth", ResourceType: "auth", ResourceID: "login"},
		{Action: "missing", ResourceType: "domain", ResourceID: "deleted"},
	} {
		entry.Status = "success"
		if err := audit.CreateAuditLog(ctx, entry); err != nil {
			t.Fatal(entry.Action, err)
		}
	}
	for _, entry := range []AuditLog{
		{Action: "forged-project", ResourceType: "domain", ResourceID: resource.ID, HistoryScope: HistoryScope{ProjectID: "history-b"}},
		{Action: "wrong-resource", ResourceType: "domain", ResourceID: "different", TaskID: task.ID},
		{Action: "missing-task", ResourceType: "domain", ResourceID: resource.ID, TaskID: "missing"},
		{Action: "wrong-system", ResourceType: "domain", ResourceID: resource.ID, TaskID: management.ID},
		{Action: "wrong-task", ResourceType: "task", ResourceID: task.ID, TaskID: management.ID},
		{Action: "false-system", ResourceType: "management_certificate", ResourceID: ManagementResourceID, TaskID: task.ID},
	} {
		t.Run(entry.Action, func(t *testing.T) {
			entry.Status = "success"
			if err := audit.CreateAuditLog(ctx, entry); err == nil {
				t.Fatal("无效审计关联未拒绝")
			}
		})
	}
	finished := time.Now().UTC()
	if err := tasks.CancelTask(ctx, task.ID, "测试取消", &finished); err != nil {
		t.Fatal(err)
	}
	if err := domains.Delete(ctx, resource.ID); err != nil {
		t.Fatal(err)
	}
	if err := projects.DeleteProject(ctx, "history-a"); err != nil {
		t.Fatal(err)
	}
	// 资源和项目删除不删除任务，也不能让收尾审计丢失原项目。
	if err := audit.CreateAuditLog(ctx, AuditLog{Action: "after-delete", ResourceType: "domain", ResourceID: resource.ID, TaskID: task.ID, Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	items, err := tasks.ListTasks(ctx, resource.ID, 10)
	if err != nil || len(items) != 1 || items[0].HistoryScope != expected || items[0].Status != "cancelled" {
		t.Fatalf("历史任务丢失：%+v，%v", items, err)
	}
	attempts, err := tasks.ListAttempts(ctx, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].HistoryScope != expected || attempts[0].Status != "cancelled" {
		t.Fatalf("尝试未继承历史范围：%+v，%v", attempts, err)
	}
	logs, err := audit.ListAuditLogs(ctx, 50)
	if err != nil || len(logs) != 7 {
		t.Fatalf("审计数量错误：%d，%v", len(logs), err)
	}
	for _, entry := range logs {
		want := expected
		if entry.Action == "system" || entry.Action == "auth" {
			want = HistoryScope{ScopeType: ScopeSystem}
		}
		if entry.Action == "missing" {
			want = HistoryScope{ScopeType: ScopeUnknown}
		}
		if entry.HistoryScope != want {
			t.Fatalf("审计快照错误：%+v", entry)
		}
		if entry.Action == "linked" && (entry.TaskID != task.ID || entry.OperatorID != "actor") {
			t.Fatalf("审计任务或操作者丢失：%+v", entry)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := tasks.CreateTask(cancelled, management); err == nil {
		t.Fatal("已取消上下文未生效")
	}
	checkHistoryAttemptBoundaries(t, domains, tasks)
}

func checkHistoryAttemptBoundaries(t *testing.T, domains *SQLDomainRepository, tasks *SQLTaskRepository) {
	t.Helper()
	for _, name := range []string{"deleted-resource", "changed-project", "unknown-parent", "orphan-attempt"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			resource, err := domain.NewDomain(name+".example.test", domain.CertificateSingle, nil)
			if err != nil {
				t.Fatal(err)
			}
			resource.ProjectID = "history-b"
			if err := domains.Create(ctx, resource); err != nil {
				t.Fatal(err)
			}
			task := Task{ID: name, DomainID: resource.ID, TaskType: "certificate_issue", Status: "running", Stage: "issue"}
			if err := tasks.CreateTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			attempt := TaskAttempt{ID: name, TaskID: task.ID, AttemptNumber: 1, Status: "running", Stage: "issue"}
			if err := tasks.CreateAttempt(ctx, attempt); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "deleted-resource":
				err = domains.Delete(ctx, resource.ID)
			case "changed-project":
				_, err = tasks.db.ExecContext(ctx, tasks.query("UPDATE domains SET project_id = ? WHERE id = ?"), "default", resource.ID)
			case "unknown-parent":
				// 模拟保留下来的未知历史任务，不通过新建接口伪造范围。
				_, err = tasks.db.ExecContext(ctx, tasks.query("UPDATE tasks SET scope_type = 'unknown', project_id = '' WHERE id = ?"), task.ID)
			case "orphan-attempt":
				_, err = tasks.db.ExecContext(ctx, tasks.query("DELETE FROM tasks WHERE id = ?"), task.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			attempt.ID += "-retry"
			attempt.AttemptNumber = 2
			if err := tasks.CreateAttempt(ctx, attempt); err == nil {
				t.Fatal("资源或历史范围失效后仍允许新增尝试")
			}
			stored, err := tasks.ListAttempts(ctx, task.ID)
			if err != nil || len(stored) != 1 || stored[0].ID != name {
				t.Fatalf("历史尝试丢失或新增拒绝后残留：%+v，%v", stored, err)
			}
			want := HistoryScope{ScopeType: ScopeProject, ProjectID: "history-b"}
			if name == "unknown-parent" || name == "orphan-attempt" {
				want = HistoryScope{ScopeType: ScopeUnknown}
			}
			if stored[0].HistoryScope != want {
				t.Fatalf("历史尝试范围被当前资源覆盖：%+v", stored[0])
			}
		})
	}
}
