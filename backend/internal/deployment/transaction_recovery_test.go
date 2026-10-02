package deployment

import (
	"context"
	"testing"
)

type memoryTransactionJournal struct {
	pending  []TransactionRecord
	finished []struct {
		id      string
		status  string
		message string
	}
	updated []TransactionRecord
}

func (j *memoryTransactionJournal) CreateDeploymentTransaction(_ context.Context, item TransactionRecord) error {
	j.pending = append(j.pending, item)
	return nil
}

func (j *memoryTransactionJournal) UpdateDeploymentTransaction(_ context.Context, item TransactionRecord) error {
	j.updated = append(j.updated, item)
	return nil
}

func (j *memoryTransactionJournal) FinishDeploymentTransaction(_ context.Context, id, status, message string) error {
	j.finished = append(j.finished, struct {
		id      string
		status  string
		message string
	}{id: id, status: status, message: message})
	return nil
}

func (j *memoryTransactionJournal) ListPendingDeploymentTransactions(context.Context) ([]TransactionRecord, error) {
	return append([]TransactionRecord(nil), j.pending...), nil
}

func TestRecoverPendingRollsBackOwnedDeployment(t *testing.T) {
	server := testServer("recovery")
	server = server.Normalize()
	client := newMemoryClient()
	id := "recovery-transaction"
	paths := []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath}
	locks := make([]string, 0, len(paths))
	backups := make([]TransactionBackup, 0, len(paths))
	temporary := make([]string, 0, len(paths))
	files := make(map[string]string, len(paths))
	for _, path := range paths {
		lockPath := path + ".scert-lock"
		backupPath := path + ".scert-backup-" + id
		temporaryPath := path + ".scert-tmp-" + id
		locks = append(locks, lockPath)
		backups = append(backups, TransactionBackup{Original: path, Saved: backupPath, OriginalExists: true, OriginalDigest: contentDigest([]byte("old"))})
		temporary = append(temporary, temporaryPath)
		client.files[lockPath] = memoryFile{}
		client.files[lockPath+"/owner"] = memoryFile{data: []byte(contentDigest([]byte("scert-lock:" + id))), mode: 0o600}
		client.files[backupPath] = memoryFile{data: []byte("old"), mode: 0o600}
		client.files[path] = memoryFile{data: []byte("new"), mode: 0o600}
		client.files[temporaryPath] = memoryFile{data: []byte("partial"), mode: 0o600}
		files[path] = contentDigest([]byte("new"))
	}
	journal := &memoryTransactionJournal{pending: []TransactionRecord{{
		ID: id, TaskID: "task", AttemptID: "attempt", AttemptNumber: 1, ResultIndex: 0,
		ServerID: server.ID, ServerName: server.Name, Paths: paths, TemporaryPaths: temporary,
		InstalledPaths: paths, Backups: backups, LockPaths: locks, ReloadAttempted: true,
		Phase: TransactionPhaseReloadAttempted, Status: TransactionStatusPending,
		Evidence: &RecoveryEvidence{Version: 1, LockToken: contentDigest([]byte("scert-lock:" + id)), Address: server.Address, Port: server.Port, HostKeyFingerprint: server.HostKeyFingerprint, ValidationCommand: append([]string(nil), server.ValidationCommand...), ReloadCommand: append([]string(nil), server.ReloadCommand...), Files: files, Baseline: RecoveryBaseline{PreviousFingerprint: "old", Endpoint: "https://example.com", Domains: []string{"example.com"}}},
	}}}
	journal.pending[0].Evidence.TemporaryFiles = make(map[string]string)
	for _, path := range temporary {
		journal.pending[0].Evidence.TemporaryFiles[path] = contentDigest([]byte("partial"))
	}
	service := NewServiceWithJournal(memoryFactory{clients: map[string]*memoryClient{server.ID: client}}, journal)
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
	if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err != nil {
		t.Fatalf("事务恢复失败：%v", err)
	}
	for _, path := range paths {
		file, ok := client.files[path]
		if !ok || string(file.data) != "old" {
			t.Fatalf("目标文件未恢复：%s，文件：%+v", path, client.files)
		}
		if _, ok := client.files[path+".scert-lock"]; ok {
			t.Fatalf("恢复成功后不应残留锁：%s", path)
		}
		if _, ok := client.files[path+".scert-tmp-"+id]; ok {
			t.Fatalf("恢复成功后不应残留临时文件：%s", path)
		}
	}
	if len(client.commands) != 2 || len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatalf("恢复收尾记录不正确：命令=%d，完成记录=%+v", len(client.commands), journal.finished)
	}
}

func TestRecoverPendingKeepsUnprovableState(t *testing.T) {
	server := testServer("unprovable")
	client := newMemoryClient()
	journal := &memoryTransactionJournal{pending: []TransactionRecord{{
		ID: "unprovable-transaction", TaskID: "task", AttemptID: "attempt", AttemptNumber: 1,
		ResultIndex: 0, ServerID: server.ID, ServerName: server.Name,
		Paths:     []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath},
		LockPaths: []string{server.PrivateKeyPath + ".scert-lock"}, Status: TransactionStatusPending,
		Phase: TransactionPhaseInstalled,
	}}}
	service := NewServiceWithJournal(memoryFactory{clients: map[string]*memoryClient{server.ID: client}}, journal)
	if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err == nil {
		t.Fatal("无法证明锁归属时必须返回人工核查错误")
	}
	if len(journal.updated) != 1 || journal.updated[0].Status != TransactionStatusRecoveryRequired {
		t.Fatalf("未记录人工核查状态：%+v", journal.updated)
	}
}

func TestDeployLockConflictFinalizesTransaction(t *testing.T) {
	server := testServer("lock-conflict").Normalize()
	client := newMemoryClient()
	blockedLock := server.CertificatePath + ".scert-lock"
	client.files[blockedLock] = memoryFile{}
	journal := &memoryTransactionJournal{}
	service := NewServiceWithJournal(memoryFactory{clients: map[string]*memoryClient{server.ID: client}}, journal)
	ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})

	report := service.Deploy(ctx, []Target{{Server: server}}, testArtifact(t))
	if report.Status != "failed" || len(report.Results) != 1 || report.Results[0].FailureCode != FailureLock {
		t.Fatalf("锁冲突应失败：%+v", report)
	}
	if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatalf("锁冲突后必须完成回滚事务：%+v", journal.finished)
	}
	if _, ok := client.files[blockedLock]; !ok {
		t.Fatal("已有锁不能被清理")
	}
	for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
		lockPath := path + ".scert-lock"
		if lockPath != blockedLock {
			if _, ok := client.files[lockPath]; ok {
				t.Fatalf("锁冲突失败后不应残留本次锁：%s", lockPath)
			}
		}
	}
}

func TestDeployPartialLockConflictReleasesOwnedLocks(t *testing.T) {
	server := testServer("partial-lock-conflict").Normalize()
	client := newMemoryClient()
	blockedLock := server.PrivateKeyPath + ".scert-lock"
	client.files[blockedLock] = memoryFile{}
	journal := &memoryTransactionJournal{}
	service := NewServiceWithJournal(memoryFactory{clients: map[string]*memoryClient{server.ID: client}}, journal)
	ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})

	report := service.Deploy(ctx, []Target{{Server: server}}, testArtifact(t))
	if report.Status != "failed" || len(report.Results) != 1 || report.Results[0].FailureCode != FailureLock {
		t.Fatalf("部分锁冲突应失败：%+v", report)
	}
	if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatalf("部分锁冲突后必须完成回滚事务：%+v", journal.finished)
	}
	if _, ok := client.files[blockedLock]; !ok {
		t.Fatal("已有锁不能被清理")
	}
	for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
		lockPath := path + ".scert-lock"
		if lockPath == blockedLock {
			continue
		}
		if _, ok := client.files[lockPath]; ok {
			t.Fatalf("部分锁冲突失败后不应残留本次锁：%s", lockPath)
		}
		if _, ok := client.files[lockPath+"/owner"]; ok {
			t.Fatalf("部分锁冲突失败后不应残留锁归属：%s", lockPath)
		}
	}
}

func TestRecoverPendingAcquiringPreservesLocksWithoutOwner(t *testing.T) {
	server := testServer("acquiring-missing-owner").Normalize()
	client := newMemoryClient()
	id := "acquiring-missing-owner-transaction"
	paths := []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath}
	locks := make([]string, 0, len(paths))
	files := make(map[string]string, len(paths))
	for _, path := range paths {
		lockPath := path + ".scert-lock"
		locks = append(locks, lockPath)
		client.files[lockPath] = memoryFile{}
		files[path] = contentDigest([]byte("old"))
	}
	journal := &memoryTransactionJournal{pending: []TransactionRecord{{
		ID: id, TaskID: "task", AttemptID: "attempt", AttemptNumber: 1, ResultIndex: 0,
		ServerID: server.ID, ServerName: server.Name, Paths: paths, LockPaths: locks,
		Phase: TransactionPhaseAcquiring, Status: TransactionStatusRecoveryRequired,
		Evidence: &RecoveryEvidence{
			Version: 1, LockToken: contentDigest([]byte("scert-lock:" + id)),
			Address: server.Address, Port: server.Port, HostKeyFingerprint: server.HostKeyFingerprint,
			ValidationCommand: append([]string(nil), server.ValidationCommand...),
			ReloadCommand:     append([]string(nil), server.ReloadCommand...), Files: files,
		},
	}}}
	service := NewServiceWithJournal(memoryFactory{clients: map[string]*memoryClient{server.ID: client}}, journal)
	if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err == nil {
		t.Fatal("无归属锁不能证明属于本次中断事务，必须拒绝自动清理")
	}
	for _, lockPath := range locks {
		if _, ok := client.files[lockPath]; !ok {
			t.Fatalf("不得删除无归属锁目录：%s", lockPath)
		}
	}
	if len(journal.finished) != 0 || len(journal.updated) != 1 || journal.updated[0].Status != TransactionStatusRecoveryRequired {
		t.Fatal("无归属锁必须保留事务等待人工核查")
	}
}
