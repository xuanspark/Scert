package deployment

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strconv"
	"testing"
)

type journalCollisionClient struct {
	*memoryClient
	collisionAt int
	calls       int
	collided    string
}

func (c *journalCollisionClient) Upload(ctx context.Context, path string, data []byte, mode uint32) error {
	c.calls++
	if c.calls == c.collisionAt {
		c.collided = path
		c.files[path] = memoryFile{data: []byte("not-owned"), mode: 0o600}
		return &uploadNotCreatedError{err: errors.New("模拟独占创建冲突")}
	}
	return c.memoryClient.Upload(ctx, path, data, mode)
}

func TestJournalUploadCollisionPreservesUnownedFile(t *testing.T) {
	for _, position := range []int{1, 3} {
		t.Run(strconv.Itoa(position), func(t *testing.T) {
			client := &journalCollisionClient{memoryClient: newMemoryClient(), collisionAt: position}
			server := testServer("journal-collision")
			client.files[server.CertificatePath] = memoryFile{data: []byte("current"), mode: 0o600}
			journal := &memoryTransactionJournal{}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
			report := service.Deploy(ctx, []Target{{Server: server}}, testArtifact(t))
			if report.Status != "failed" || client.collided == "" {
				t.Fatalf("必须触发上传碰撞：%+v", report)
			}
			if string(client.files[client.collided].data) != "not-owned" {
				t.Error("事务日志不得把计划路径当作已取得所有权的临时文件删除")
			}
			if string(client.files[server.CertificatePath].data) != "current" || len(client.commands) != 0 {
				t.Fatal("上传碰撞不得改变当前证书或执行命令")
			}
			if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRecoveryRequired {
				t.Fatal("碰撞路径归属未知，必须保留锁及事务等待核查")
			}
			for _, target := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
				if _, exists := client.files[target+".scert-lock"]; !exists {
					t.Fatal("未知文件仍存在时不得自动释放部署锁")
				}
			}
		})
	}
}

func TestRecoveryRejectsUnprovenTemporaryFileBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"changed", "unrecorded", "commit_changed", "legacy", "legacy_commit"} {
		t.Run(scenario, func(t *testing.T) {
			server, client, record := recoveryPreflightFixture()
			switch scenario {
			case "legacy", "legacy_commit":
				record.Evidence.TemporaryFiles = nil
			case "unrecorded":
				delete(record.Evidence.TemporaryFiles, record.TemporaryPaths[2])
			}
			if scenario == "commit_changed" || scenario == "legacy_commit" {
				record.Phase = TransactionPhaseCommitting
			}
			client.files[record.TemporaryPaths[2]] = memoryFile{data: []byte("foreign-file"), mode: 0o600}
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
			before := maps.Clone(client.files)
			err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil })
			if err == nil || !reflect.DeepEqual(client.files, before) || len(client.commands) != 0 {
				t.Fatal("无法证明临时文件身份时必须保留完整现场，不得先删除其他文件")
			}
		})
	}
}

func TestJournalPartialUploadCleansOnlyProvenFiles(t *testing.T) {
	client := newMemoryClient()
	client.uploadError = "key.pem"
	server := testServer("journal-partial")
	for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
		client.files[path] = memoryFile{data: []byte("original"), mode: 0o600}
	}
	before := maps.Clone(client.files)
	journal := &memoryTransactionJournal{}
	service := NewServiceWithJournal(fixedClientFactory{client}, journal)
	ctx := WithTransactionContext(t.Context(), TransactionContext{TaskID: "task", AttemptID: "attempt", AttemptNumber: 1})
	report := service.Deploy(ctx, []Target{{Server: server}}, testArtifact(t))
	if report.Status != "failed" || !reflect.DeepEqual(client.files, before) || len(client.commands) != 0 {
		t.Fatalf("已取得归属的半份私钥应可安全清理，且不改变旧证书：%+v", report)
	}
	if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
		t.Fatal("已证明归属的上传失败应完成回滚")
	}
}

func TestRecoveryLegacyWithoutTemporaryFilesStillRestores(t *testing.T) {
	server, client, record := recoveryPreflightFixture()
	record.Evidence.TemporaryFiles = nil
	for _, path := range record.TemporaryPaths {
		delete(client.files, path)
	}
	journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
	service := NewServiceWithJournal(fixedClientFactory{client}, journal)
	service.VerifyRecoveryTarget = func(context.Context, Server, RecoveryBaseline, bool) error { return nil }
	if err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil }); err != nil {
		t.Fatalf("没有残留临时文件且其他证据完整的旧事务仍应恢复：%v", err)
	}
}

func TestRecoveryRejectsMalformedTemporaryOwnershipBeforeAuthentication(t *testing.T) {
	for _, scenario := range []string{"foreign_path", "invalid_digest", "duplicate_path"} {
		t.Run(scenario, func(t *testing.T) {
			server, _, record := recoveryPreflightFixture()
			switch scenario {
			case "foreign_path":
				record.Evidence.TemporaryFiles["/foreign/file"] = contentDigest([]byte("foreign"))
			case "invalid_digest":
				record.Evidence.TemporaryFiles[record.TemporaryPaths[0]] = "invalid"
			case "duplicate_path":
				record.TemporaryPaths = append(record.TemporaryPaths, record.TemporaryPaths[0])
			}
			if err := validateRecoveryEvidence(record, server); err == nil {
				t.Fatal("认证前必须拒绝无效临时文件证据")
			}
		})
	}
}
