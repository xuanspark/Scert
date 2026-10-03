package repository

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// 重开真实隔离数据库，验证审计失败不丢失占用，也不将只读核查变成远端重放。
func runExternalReconciliationAuditFailure(t *testing.T, fixture recoveryExternalFixture, action deployment.RecoveryAction, stage string) {
	t.Helper()
	cfg := fixture.database(t)
	db := openRecoveryExternal(t, cfg)
	if err := database.Migrate(t.Context(), db, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLDeploymentTransactionRepository(db, cfg.Driver)
	service, client, server, record, source := seedUncertainReconciliation(t, repo, db, action)
	files := maps.Clone(client.files)
	resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
	confirmation, token, err := service.PrepareRecoveryReconciliation(t.Context(), source.Intent.ID, "unix:0", resolve)
	if err != nil {
		t.Fatal(err)
	}
	fixture.inject(t, db, cfg, stage)
	result, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
	if !errors.Is(err, deployment.ErrRecoveryDispositionUnavailable) || result.Status == "succeeded" {
		t.Fatal("重新核查审计失败发布成功结果", err)
	}
	if client.writes != 0 || !reflect.DeepEqual(files, client.files) {
		t.Fatal("审计失败核查修改远端文件")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openRecoveryExternal(t, cfg)
	repo = NewSQLDeploymentTransactionRepository(db, cfg.Driver)
	assertReconciliationSource(t, repo, source)
	recoveryCount(t, db, "recovery_claims", 1)
	wantOwner := source.Intent.ID
	if stage == "finished" {
		wantOwner = confirmation.DispositionID()
		recoveryCount(t, db, "recovery_dispositions", 2)
		recoveryCount(t, db, "recovery_events", 3)
		stored, err := repo.GetRecoveryDisposition(t.Context(), wantOwner)
		if err != nil || stored.Status != "running" || stored.ResultCode != "" || stored.Intent.SourceDispositionID != source.Intent.ID {
			t.Fatal("结束审计失败未持久保留运行中处置", err)
		}
	} else {
		recoveryCount(t, db, "recovery_dispositions", 1)
		recoveryCount(t, db, "recovery_events", 2)
	}
	var owner string
	if err := db.QueryRowContext(t.Context(), repo.query("SELECT disposition_id FROM recovery_claims WHERE transaction_id = ?"), record.ID).Scan(&owner); err != nil || owner != wantOwner {
		t.Fatal("重开后占用归属错误", err)
	}
	pending, err := repo.ListPendingDeploymentTransactions(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != deployment.TransactionStatusRecoveryRequired {
		t.Fatal("审计失败改写了原事务终态", err)
	}
	restarted := deployment.NewServiceWithJournal(nil, repo)
	if err := restarted.RecoverPending(t.Context(), func(context.Context, string) (deployment.Server, error) {
		t.Error("重开后自动恢复试图重放已占用事务")
		return deployment.Server{}, errors.New("禁止连接远端")
	}); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
		t.Fatal("重开后自动恢复未遵守持久占用", err)
	}
}
