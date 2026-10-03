package repository

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"reflect"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// 远端文件是测试替身；事务、关联、占用交接与审计均经过真实数据库和产品执行器。
type reconciliationClient struct {
	*executorIntegrationClient
	failClosed, readOnly bool
	connections, writes  int
}

func (c *reconciliationClient) Connect(context.Context, deployment.Server) (deployment.Client, error) {
	c.connections++
	return c, nil
}
func (c *reconciliationClient) Close() error {
	if c.failClosed {
		if _, exists := c.files["/cert.pem.scert-lock"]; !exists {
			return errors.New("注入动作完成后的连接关闭失败")
		}
	}
	return nil
}
func (c *reconciliationClient) writable() error {
	c.writes++
	if c.readOnly {
		return errors.New("重新核查不得执行远端写操作")
	}
	return nil
}
func (c *reconciliationClient) Remove(ctx context.Context, path string) error {
	if err := c.writable(); err != nil {
		return err
	}
	return c.executorIntegrationClient.Remove(ctx, path)
}
func (c *reconciliationClient) Rename(ctx context.Context, from, to string) error {
	if err := c.writable(); err != nil {
		return err
	}
	return c.executorIntegrationClient.Rename(ctx, from, to)
}
func (c *reconciliationClient) Unlock(ctx context.Context, path string) error {
	if err := c.writable(); err != nil {
		return err
	}
	return c.executorIntegrationClient.Unlock(ctx, path)
}
func (c *reconciliationClient) Run(ctx context.Context, command []string) (deployment.CommandResult, error) {
	if err := c.writable(); err != nil {
		return deployment.CommandResult{}, err
	}
	return c.executorIntegrationClient.Run(ctx, command)
}

func seedUncertainReconciliation(t *testing.T, repo *SQLDeploymentTransactionRepository, db *sql.DB, action deployment.RecoveryAction) (*deployment.Service, *reconciliationClient, deployment.Server, deployment.TransactionRecord, deployment.RecoveryDisposition) {
	t.Helper()
	_, _, _, remote, server, record := seedRecoveryExecutor(t, repo, db)
	client := &reconciliationClient{executorIntegrationClient: remote, failClosed: true}
	service := deployment.NewServiceWithJournal(client, repo)
	service.VerifyRecoveryTarget = func(context.Context, deployment.Server, deployment.RecoveryBaseline, bool) error { return nil }
	resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
	confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
	if !errors.Is(err, deployment.ErrRecoveryActionUncertain) || source.Status != deployment.RecoveryResultUncertain {
		t.Fatalf("未形成真实执行器不确定结果：%+v %v", source, err)
	}
	client.failClosed, client.readOnly = false, true
	client.writes = 0
	return service, client, server, record, source
}

func assertReconciliationSource(t *testing.T, repo *SQLDeploymentTransactionRepository, source deployment.RecoveryDisposition) {
	t.Helper()
	stored, err := repo.GetRecoveryDisposition(t.Context(), source.Intent.ID)
	if err != nil || !reflect.DeepEqual(stored, source) {
		t.Fatalf("重新核查改写了原不确定历史：%+v %v", stored, err)
	}
}

func runReconciliationSuccess(t *testing.T, repo *SQLDeploymentTransactionRepository, db *sql.DB, action deployment.RecoveryAction) {
	t.Helper()
	service, client, server, record, source := seedUncertainReconciliation(t, repo, db, action)
	resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
	files := maps.Clone(client.files)
	verified := false
	service.VerifyRecoveryTarget = func(_ context.Context, _ deployment.Server, _ deployment.RecoveryBaseline, committed bool) error {
		if committed != (action == deployment.RecoveryActionCommit) {
			t.Fatal("重新核查擅自切换方向")
		}
		verified = true
		return nil
	}
	confirmation, token, err := service.PrepareRecoveryReconciliation(t.Context(), source.Intent.ID, "unix:0", resolve)
	if err != nil || confirmation.Summary().Validate() != nil {
		t.Fatal("准备重新核查失败", err)
	}
	other, otherToken, err := service.PrepareRecoveryReconciliation(t.Context(), source.Intent.ID, "unix:0", resolve)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
	if err != nil || result.Status != "succeeded" || result.Intent.SourceDispositionID != source.Intent.ID || !verified {
		t.Fatalf("重新核查失败：%+v %v", result, err)
	}
	if client.writes != 0 || !reflect.DeepEqual(files, client.files) {
		t.Fatal("只读重新核查修改了远端")
	}
	assertReconciliationSource(t, repo, source)
	recoveryCount(t, db, "recovery_dispositions", 2)
	recoveryCount(t, db, "recovery_events", 4)
	recoveryCount(t, db, "recovery_claims", 0)
	pending, err := repo.ListPendingDeploymentTransactions(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal("原事务未原子结束", err)
	}
	view, err := service.GetRecoveryDisposition(t.Context(), result.Intent.ID)
	if err != nil || view.SourceDispositionID != source.Intent.ID || view.Validate() != nil {
		t.Fatal("查询缺少可信关联", err)
	}
	connections := client.connections
	if _, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve); !errors.Is(err, deployment.ErrRecoveryConfirmation) {
		t.Fatal("重新核查确认被重放", err)
	}
	if _, err := service.ExecuteRecoveryDisposition(t.Context(), other, "unix:0", otherToken, resolve); err == nil {
		t.Fatal("其他旧确认再次接管已完成的事务")
	}
	if client.connections != connections {
		t.Fatal("完成后仍使用旧确认连接远端")
	}
	if record.ID != result.Intent.TransactionID {
		t.Fatal("结束了其他事务")
	}
}

func TestRecoveryReconciliationSQLiteSuccess(t *testing.T) {
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			_, db := newTaskRepositoryTest(t)
			t.Cleanup(func() { _ = db.Close() })
			runReconciliationSuccess(t, NewSQLDeploymentTransactionRepository(db), db, action)
		})
	}
}

func TestRecoveryReconciliationSQLiteRejectsUnsafeCompletion(t *testing.T) {
	for _, failure := range []string{"file", "backup", "temporary", "lock", "https", "during-https", "close", "changed-after-prepare", "started-audit", "finished-audit", "identity", "phase", "running", "direction"} {
		t.Run(failure, func(t *testing.T) {
			_, db := newTaskRepositoryTest(t)
			t.Cleanup(func() { _ = db.Close() })
			repo := NewSQLDeploymentTransactionRepository(db)
			service, client, server, record, source := seedUncertainReconciliation(t, repo, db, deployment.RecoveryActionCommit)
			resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
			switch failure {
			case "file":
				client.files[record.Paths[0]] = "tampered"
			case "backup":
				client.files[record.Backups[0].Saved] = "old"
			case "temporary":
				client.files[record.Paths[0]+".scert-tmp-"+record.ID] = "unexpected"
			case "lock":
				client.files[record.LockPaths[0]] = ""
			case "https":
				service.VerifyRecoveryTarget = func(context.Context, deployment.Server, deployment.RecoveryBaseline, bool) error {
					return errors.New("严格 HTTPS 未通过")
				}
			case "during-https":
				service.VerifyRecoveryTarget = func(context.Context, deployment.Server, deployment.RecoveryBaseline, bool) error {
					client.files[record.Paths[0]] = "changed"
					return nil
				}
			case "identity":
				server.PasswordRef = "secret://ssh/password/changed"
			case "phase":
				if _, err := db.ExecContext(t.Context(), `UPDATE deployment_transactions SET phase = 'reload_attempted'`); err != nil {
					t.Fatal(err)
				}
			case "running":
				if _, err := db.ExecContext(t.Context(), `UPDATE recovery_dispositions SET status = 'running', result_code = ''`); err != nil {
					t.Fatal(err)
				}
			case "direction":
				if _, err := db.ExecContext(t.Context(), `UPDATE recovery_dispositions SET action = 'restore_baseline'`); err != nil {
					t.Fatal(err)
				}
			}
			confirmation, token, err := service.PrepareRecoveryReconciliation(t.Context(), source.Intent.ID, "unix:0", resolve)
			if failure == "identity" || failure == "phase" || failure == "running" || failure == "direction" {
				if err == nil {
					t.Fatal("危险来源允许重新核查")
				}
				recoveryCount(t, db, "recovery_claims", 1)
				return
			}
			if err != nil {
				t.Fatal("准备失败", err)
			}
			if failure == "close" {
				service.VerifyRecoveryTarget = func(context.Context, deployment.Server, deployment.RecoveryBaseline, bool) error {
					client.failClosed = true
					return nil
				}
			}
			if failure == "changed-after-prepare" {
				client.files[record.Paths[1]] = "changed"
			}
			if failure == "started-audit" || failure == "finished-audit" {
				stage := "started"
				if failure == "finished-audit" {
					stage = "finished"
				}
				if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_recheck BEFORE INSERT ON recovery_events WHEN NEW.event_type = '`+stage+`' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
			if err == nil || result.Status == "succeeded" || client.writes != 0 {
				t.Fatalf("危险收尾未拒绝或执行了写操作：%+v %v", result, err)
			}
			assertReconciliationSource(t, repo, source)
			var owner string
			if err := db.QueryRowContext(t.Context(), `SELECT disposition_id FROM recovery_claims WHERE transaction_id = ?`, record.ID).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			expected := source.Intent.ID
			if failure == "finished-audit" {
				expected = confirmation.DispositionID()
			}
			if owner != expected {
				t.Fatal("失败后占用归属不正确", owner, expected)
			}
			pending, err := repo.ListPendingDeploymentTransactions(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Status != deployment.TransactionStatusRecoveryRequired {
				t.Fatal("失败改变了原事务", err)
			}
		})
	}
}
