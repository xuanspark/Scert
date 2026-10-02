package deployment

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"testing"
)

func recoveryPreflightFixture(roots ...string) (Server, *memoryClient, TransactionRecord) {
	server := testServer("preflight").Normalize()
	if len(roots) == 1 {
		server.CertificatePath = filepath.ToSlash(filepath.Join(roots[0], "cert.pem"))
		server.CertificateChainPath = filepath.ToSlash(filepath.Join(roots[0], "fullchain.pem"))
		server.PrivateKeyPath = filepath.ToSlash(filepath.Join(roots[0], "key.pem"))
	}
	client := newMemoryClient()
	r := TransactionRecord{
		ID: "preflight-transaction", TaskID: "task", AttemptID: "attempt", AttemptNumber: 1,
		ServerID: server.ID, ServerName: server.Name,
		Paths: []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath},
		Phase: TransactionPhaseReloadAttempted, Status: TransactionStatusPending, ReloadAttempted: true,
		Evidence: &RecoveryEvidence{
			Version: 1, LockToken: contentDigest([]byte("preflight-owner")),
			Address: server.Address, Port: server.Port, HostKeyFingerprint: server.HostKeyFingerprint,
			ValidationCommand: server.ValidationCommand, ReloadCommand: server.ReloadCommand,
			Files: map[string]string{}, TemporaryFiles: map[string]string{}, Baseline: RecoveryBaseline{
				Endpoint: "https://example.com", Domains: []string{"example.com"},
				PreviousFingerprint: "old", ExpectedFingerprint: "new",
			},
		},
	}
	for _, path := range r.Paths {
		lock, backup, temporary := path+".scert-lock", path+".scert-backup-"+r.ID, path+".scert-tmp-"+r.ID
		r.LockPaths = append(r.LockPaths, lock)
		r.TemporaryPaths = append(r.TemporaryPaths, temporary)
		r.InstalledPaths = append(r.InstalledPaths, path)
		r.Backups = append(r.Backups, TransactionBackup{
			Original: path, Saved: backup, OriginalExists: true, OriginalDigest: contentDigest([]byte("old")),
		})
		r.Evidence.Files[path] = contentDigest([]byte("new"))
		r.Evidence.TemporaryFiles[temporary] = contentDigest([]byte("partial"))
		client.files[lock] = memoryFile{}
		client.files[lock+"/owner"] = memoryFile{data: []byte(r.Evidence.LockToken), mode: 0o600}
		client.files[backup] = memoryFile{data: []byte("old"), mode: 0o600}
		client.files[path] = memoryFile{data: []byte("new"), mode: 0o600}
		client.files[temporary] = memoryFile{data: []byte("partial"), mode: 0o600}
	}
	return server, client, r
}

func TestRecoveryPreflightRejectsUntrustedStateWithoutMutation(t *testing.T) {
	for _, scenario := range []string{
		"foreign_lock", "missing_owner", "changed_backup", "changed_target", "missing_backup_and_target",
		"commit_changed_backup", "acquiring_without_owner", "verified_foreign_lock",
	} {
		t.Run(scenario, func(t *testing.T) {
			server, client, r := recoveryPreflightFixture()
			switch scenario {
			case "foreign_lock", "verified_foreign_lock":
				client.files[r.LockPaths[0]+"/owner"] = memoryFile{data: []byte(contentDigest([]byte("another-owner")))}
				if scenario == "verified_foreign_lock" {
					r.Phase = TransactionPhaseRollbackVerified
				}
			case "missing_owner", "acquiring_without_owner":
				delete(client.files, r.LockPaths[0]+"/owner")
				if scenario == "acquiring_without_owner" {
					r.Phase = TransactionPhaseAcquiring
				}
			case "changed_backup":
				client.files[r.Backups[0].Saved] = memoryFile{data: []byte("unexpected-backup")}
			case "changed_target":
				client.files[r.Paths[0]] = memoryFile{data: []byte("unexpected-target")}
			case "missing_backup_and_target":
				delete(client.files, r.Backups[0].Saved)
				delete(client.files, r.Paths[0])
			case "commit_changed_backup":
				r.Phase = TransactionPhaseCommitting
				client.files[r.Backups[2].Saved] = memoryFile{data: []byte("unexpected-backup")}
			}
			before := maps.Clone(client.files)
			journal := &memoryTransactionJournal{pending: []TransactionRecord{r}}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
			err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil })
			if err == nil {
				t.Error("证据不可信时必须拒绝恢复")
			}
			if !reflect.DeepEqual(before, client.files) || len(client.commands) != 0 {
				t.Error("证据核验失败前不得修改文件、释放锁或执行命令")
			}
			if len(journal.finished) != 0 || len(journal.updated) != 1 || journal.updated[0].Status != TransactionStatusRecoveryRequired {
				t.Fatalf("必须保留事务等待人工核查：%+v", journal)
			}
		})
	}
}

func TestRecoveryVerifiedCleanupIsRestartable(t *testing.T) {
	for _, phase := range []string{TransactionPhaseRollbackVerified, TransactionPhaseCommitVerified} {
		t.Run(phase, func(t *testing.T) {
			server, client, r := recoveryPreflightFixture()
			r.Phase = phase
			delete(client.files, r.LockPaths[2])
			delete(client.files, r.LockPaths[2]+"/owner")
			journal := &memoryTransactionJournal{pending: []TransactionRecord{r}}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err != nil {
				t.Fatalf("已核验事务的部分解锁必须可重入：%v", err)
			}
			want := TransactionStatusRolledBack
			if phase == TransactionPhaseCommitVerified {
				want = TransactionStatusCommitted
			}
			if len(journal.finished) != 1 || journal.finished[0].status != want {
				t.Fatalf("收尾状态不正确：%+v", journal.finished)
			}
		})
	}
}

func TestRecoveryPreflightAllowsInterruptedFileStates(t *testing.T) {
	for _, scenario := range []string{"before_backup_rename", "after_backup_rename", "partly_restored", "new_target", "commit_partly_cleaned", "acquiring_partly_locked"} {
		t.Run(scenario, func(t *testing.T) {
			server, client, r := recoveryPreflightFixture()
			switch scenario {
			case "before_backup_rename", "partly_restored":
				delete(client.files, r.Backups[0].Saved)
				client.files[r.Paths[0]] = memoryFile{data: []byte("old"), mode: 0o600}
			case "after_backup_rename":
				delete(client.files, r.Paths[0])
			case "new_target":
				r.Backups[0].OriginalExists = false
				r.Backups[0].OriginalDigest = ""
				delete(client.files, r.Backups[0].Saved)
			case "commit_partly_cleaned":
				r.Phase = TransactionPhaseCommitting
				delete(client.files, r.Backups[0].Saved)
			case "acquiring_partly_locked":
				r.Phase = TransactionPhaseAcquiring
				delete(client.files, r.LockPaths[2])
				delete(client.files, r.LockPaths[2]+"/owner")
			}
			journal := &memoryTransactionJournal{pending: []TransactionRecord{r}}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			service.VerifyRecoveryTarget = func(_ context.Context, _ Server, _ RecoveryBaseline, committed bool) error {
				if committed != (scenario == "commit_partly_cleaned") {
					t.Fatal("恢复核验方向错误")
				}
				return nil
			}
			if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err != nil {
				t.Fatalf("可信中断状态必须可以恢复：%v", err)
			}
			wantStatus := TransactionStatusRolledBack
			if scenario == "commit_partly_cleaned" {
				wantStatus = TransactionStatusCommitted
			}
			if len(journal.finished) != 1 || journal.finished[0].status != wantStatus {
				t.Fatalf("恢复状态不正确：%+v", journal.finished)
			}
			for index, target := range r.Paths {
				if _, ok := client.files[r.LockPaths[index]]; ok {
					t.Fatal("可信恢复完成后不应残留锁")
				}
				if scenario == "new_target" && index == 0 {
					if _, ok := client.files[target]; ok {
						t.Fatal("新增目标未回滚")
					}
					continue
				}
				want := "old"
				if scenario == "commit_partly_cleaned" || scenario == "acquiring_partly_locked" {
					want = "new"
				}
				if string(client.files[target].data) != want {
					t.Fatal("目标文件恢复内容不正确")
				}
			}
		})
	}
}

func TestRecoveryPreflightHTTPSFailureKeepsLocksUntilReverified(t *testing.T) {
	server, client, r := recoveryPreflightFixture()
	journal := &memoryTransactionJournal{pending: []TransactionRecord{r}}
	service := NewServiceWithJournal(fixedClientFactory{client}, journal)
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error {
		return errors.New("模拟旧证书未实际生效")
	}
	resolve := func(context.Context, string) (Server, error) { return server, nil }
	if err := service.RecoverPending(t.Context(), resolve); err == nil {
		t.Fatal("HTTPS 核验失败不得宣称恢复成功")
	}
	if len(journal.finished) != 0 || len(journal.updated) != 1 || journal.updated[0].Status != TransactionStatusRecoveryRequired {
		t.Fatal("HTTPS 核验失败必须保留待核查事务")
	}
	for index, target := range r.Paths {
		if string(client.files[target].data) != "old" {
			t.Fatal("文件应已恢复为旧版本")
		}
		if _, ok := client.files[r.LockPaths[index]]; !ok {
			t.Fatal("站点未核验时不得释放部署锁")
		}
	}
	journal.pending = append([]TransactionRecord(nil), journal.updated...)
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
	if err := service.RecoverPending(t.Context(), resolve); err != nil {
		t.Fatalf("再次核验可信旧文件应可恢复：%v", err)
	}
	if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatal("再次核验后没有完成回滚")
	}
}

type recoveryConnectCounter struct{ calls int }

func (f *recoveryConnectCounter) Connect(context.Context, Server) (Client, error) {
	f.calls++
	return nil, errors.New("不应尝试连接身份已变更的服务器")
}

func TestRecoveryPreflightChecksIdentityBeforeAuthentication(t *testing.T) {
	for _, scenario := range []string{"address", "port", "host_key", "command", "missing_evidence"} {
		t.Run(scenario, func(t *testing.T) {
			server, _, r := recoveryPreflightFixture()
			switch scenario {
			case "address":
				server.Address = "192.0.2.55"
			case "port":
				server.Port++
			case "host_key":
				server.HostKeyFingerprint = "SHA256:changed"
			case "command":
				server.ReloadCommand = []string{"true"}
			case "missing_evidence":
				r.Evidence = nil
			}
			factory := &recoveryConnectCounter{}
			journal := &memoryTransactionJournal{pending: []TransactionRecord{r}}
			service := NewServiceWithJournal(factory, journal)
			if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err == nil {
				t.Fatal("恢复身份不一致时必须拒绝恢复")
			}
			if factory.calls != 0 {
				t.Fatal("验证恢复身份之前不得向服务器发送认证请求")
			}
		})
	}
}
