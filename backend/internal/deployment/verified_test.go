package deployment

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type lateCancellationContext struct {
	context.Context
	armed    atomic.Bool
	errCalls atomic.Int32
}

func (c *lateCancellationContext) Done() <-chan struct{} {
	return nil
}

func (c *lateCancellationContext) Err() error {
	if c.armed.Load() && c.errCalls.Add(1) > 1 {
		return context.Canceled
	}
	return nil
}

type contextCheckingTransactionJournal struct {
	memoryTransactionJournal
	commitContextOK bool
}

func (j *contextCheckingTransactionJournal) UpdateDeploymentTransaction(ctx context.Context, item TransactionRecord) error {
	if item.Phase == "committing" {
		j.commitContextOK = ctx.Err() == nil
	}
	return j.memoryTransactionJournal.UpdateDeploymentTransaction(ctx, item)
}

func TestVerifiedDeploymentCommitUsesIndependentContextAfterCancellationRace(t *testing.T) {
	client, server := newMemoryClient(), testServer("commit-context")
	seedVerifiedFiles(client, server)
	journal := &contextCheckingTransactionJournal{}
	parent := &lateCancellationContext{Context: t.Context()}
	ctx := WithTransactionContext(parent, TransactionContext{
		TaskID: "task", AttemptID: "attempt", AttemptNumber: 1,
	})
	artifact := testArtifact(t)
	service := NewServiceWithJournal(fixedClientFactory{client}, journal)
	report, err := service.DeployVerified(ctx, []Target{{Server: server}}, artifact, Verification{
		Verify: func(context.Context) error { parent.armed.Store(true); return nil },
	})
	if err != nil || report.Status != "success" || !journal.commitContextOK {
		t.Fatalf("unexpected deployment result: %+v, %v", report, err)
	}
	if parent.Err() != context.Canceled {
		t.Fatal("parent context was not cancelled after verification")
	}
	if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusCommitted {
		t.Fatalf("unexpected journal completion: %+v", journal.finished)
	}
	assertVerifiedLocks(t, client, server, false)
	for path := range client.files {
		if strings.Contains(path, ".scert-backup-") || strings.Contains(path, ".scert-tmp-") {
			t.Fatalf("transaction artifact remains: %s", path)
		}
	}
}
func seedVerifiedFiles(client *memoryClient, server Server) map[string]memoryFile {
	files := map[string]memoryFile{
		server.CertificatePath:      {data: []byte("原证书"), mode: 0o644},
		server.CertificateChainPath: {data: []byte("原证书链"), mode: 0o644},
		server.PrivateKeyPath:       {data: []byte("原私钥"), mode: 0o600},
	}
	for path, file := range files {
		client.files[path] = file
	}
	return files
}

func assertVerifiedFiles(t *testing.T, client *memoryClient, expected map[string]memoryFile) {
	t.Helper()
	for path, file := range expected {
		actual, ok := client.files[path]
		if !ok || !bytes.Equal(actual.data, file.data) || actual.mode != file.mode {
			t.Fatalf("未恢复原内容或权限：%s", path)
		}
	}
}

func assertVerifiedLocks(t *testing.T, client *memoryClient, server Server, held bool) {
	t.Helper()
	for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
		_, exists := client.files[path+".scert-lock"]
		if exists != held {
			t.Fatalf("部署锁状态不符合预期：%s，锁定 %t", path, exists)
		}
	}
}

func TestVerifiedDeploymentDefersCommitUntilAllTargetsAndSitePass(t *testing.T) {
	clients := map[string]*memoryClient{"first": newMemoryClient(), "second": newMemoryClient()}
	servers := []Server{testServer("first"), testServer("second")}
	for _, server := range servers {
		seedVerifiedFiles(clients[server.ID], server)
	}
	artifact := testArtifact(t)
	called := false
	service := NewService(memoryFactory{clients: clients})
	report, err := service.DeployVerified(t.Context(), []Target{{Server: servers[0]}, {Server: servers[1]}}, artifact, Verification{
		Verify: func(context.Context) error {
			called = true
			for _, server := range servers {
				client := clients[server.ID]
				assertVerifiedLocks(t, client, server, true)
				if !bytes.Equal(client.files[server.CertificatePath].data, artifact.Certificate) || len(client.commands) != 2 {
					t.Fatal("必须在所有服务器推送和重载后才核验站点")
				}
				backups := 0
				for path := range client.files {
					if strings.Contains(path, ".scert-backup-") {
						backups++
					}
				}
				if backups != 3 {
					t.Fatal("站点确认前必须保留三份事务旧文件")
				}
			}
			return nil
		},
		VerifyRecovery: func(context.Context) error { t.Fatal("成功部署不得回滚"); return nil },
	})
	if err != nil || !called || report.Status != "success" || report.Success != 2 || report.VerificationStatus != "success" || report.RecoveryStatus != "" {
		t.Fatalf("事务提交结果错误：%+v，%v", report, err)
	}
	for _, server := range servers {
		client := clients[server.ID]
		assertVerifiedLocks(t, client, server, false)
		if len(client.files) != 3 {
			t.Fatal("提交后不应残留事务文件")
		}
	}
}

func TestVerifiedDeploymentFailureRestoresAllTargetsBeforeRecoveryCheck(t *testing.T) {
	clients := map[string]*memoryClient{"first": newMemoryClient(), "second": newMemoryClient()}
	servers := []Server{testServer("first"), testServer("second")}
	originals := map[string]map[string]memoryFile{}
	for _, server := range servers {
		originals[server.ID] = seedVerifiedFiles(clients[server.ID], server)
	}
	recovered := false
	verificationErr := errors.New("公网仍返回错误证书")
	report, err := NewService(memoryFactory{clients: clients}).DeployVerified(t.Context(), []Target{{Server: servers[0]}, {Server: servers[1]}}, testArtifact(t), Verification{
		Verify: func(context.Context) error { return verificationErr },
		VerifyRecovery: func(ctx context.Context) error {
			recovered = true
			if _, ok := ctx.Deadline(); !ok || ctx.Err() != nil {
				t.Fatal("恢复核验必须有独立有效期限")
			}
			for _, server := range servers {
				client := clients[server.ID]
				assertVerifiedFiles(t, client, originals[server.ID])
				assertVerifiedLocks(t, client, server, true)
				expectedCommands := [][]string{{"nginx", "-t"}, {"nginx", "-s", "reload"}, {"nginx", "-t"}, {"nginx", "-s", "reload"}}
				if !reflect.DeepEqual(client.commands, expectedCommands) {
					t.Fatalf("必须恢复配置并重载后核验站点：实际=%#v，期望=%#v", client.commands, expectedCommands)
				}
			}
			return nil
		},
	})
	if !errors.Is(err, verificationErr) || !recovered || report.Status != "failed" || report.Success != 0 || report.Failed != 2 || report.VerificationStatus != "failed" || report.RecoveryStatus != "success" {
		t.Fatalf("失败与恢复状态错误：%+v，%v", report, err)
	}
	for index, server := range servers {
		assertVerifiedFiles(t, clients[server.ID], originals[server.ID])
		assertVerifiedLocks(t, clients[server.ID], server, false)
		if report.Results[index].FailureCode != FailureRecoveryRestored {
			t.Fatalf("应保留部署失败并提供恢复诊断：%+v", report.Results[index])
		}
	}
}

func TestVerifiedDeploymentCannotClaimRecoveryOnIncompleteEvidence(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		configure func(*memoryClient)
		recovery  func(context.Context) error
		status    string
		code      FailureCode
	}{
		{"missing_baseline", func(*memoryClient) {}, nil, "unconfirmed", FailureRecoveryUnknown},
		{"wrong_runtime", func(*memoryClient) {}, func(context.Context) error { return errors.New("secret-path 旧站点身份不符") }, "unconfirmed", FailureRecoveryUnknown},
		{"restore_failed", func(c *memoryClient) { c.restoreError = true }, nil, "failed", FailureRollback},
		{"validation_failed", func(c *memoryClient) { c.runResults = []CommandResult{{}, {}, {ExitCode: 1}} }, nil, "failed", FailureRecoveryUnknown},
		{"reload_failed", func(c *memoryClient) { c.runResults = []CommandResult{{}, {}, {}, {ExitCode: 1}} }, nil, "failed", FailureRecoveryUnknown},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client, server := newMemoryClient(), testServer("recovery")
			seedVerifiedFiles(client, server)
			scenario.configure(client)
			report, err := NewService(fixedClientFactory{client}).DeployVerified(t.Context(), []Target{{Server: server}}, testArtifact(t), Verification{
				Verify: func(context.Context) error { return errors.New("部署后校验失败") }, VerifyRecovery: scenario.recovery,
			})
			if err == nil || report.RecoveryStatus != scenario.status || report.Results[0].FailureCode != scenario.code {
				t.Fatalf("不得夸大恢复能力：%+v，%v", report, err)
			}
			assertVerifiedLocks(t, client, server, true)
			public := PublicResult(report.Results[0])
			if strings.Contains(public.Message, "secret-path") || public.Message == "证书部署失败" {
				t.Fatalf("恢复诊断应安全且明确：%+v", public)
			}
		})
	}
}

func TestVerifiedDeploymentCancellationStillRestoresWithBoundedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client, server := newMemoryClient(), testServer("cancel")
	original := seedVerifiedFiles(client, server)
	report, err := NewService(fixedClientFactory{client}).DeployVerified(ctx, []Target{{Server: server}}, testArtifact(t), Verification{
		Verify: func(context.Context) error { cancel(); return nil },
		VerifyRecovery: func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Fatal("请求取消不得取消恢复核验")
			}
			assertVerifiedFiles(t, client, original)
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) || report.Status != "failed" || report.RecoveryStatus != "success" || len(client.commands) != 4 {
		t.Fatalf("取消后应恢复但不得转为部署成功：%+v，%v", report, err)
	}
	assertVerifiedLocks(t, client, server, false)
}

func TestVerifiedDeploymentPartialPreparationDoesNotCommitEarlierTarget(t *testing.T) {
	client, first := newMemoryClient(), testServer("first")
	second := testServer("second")
	original := seedVerifiedFiles(client, first)
	service := NewService(memoryFactory{clients: map[string]*memoryClient{"first": client}, errors: map[string]error{"second": errors.New("连接失败")}})
	report, err := service.DeployVerified(t.Context(), []Target{{Server: first}, {Server: second}}, testArtifact(t), Verification{
		Verify:         func(context.Context) error { t.Fatal("部分准备失败不得进行提交校验"); return nil },
		VerifyRecovery: func(context.Context) error { assertVerifiedFiles(t, client, original); return nil },
	})
	if err == nil || report.Status != "failed" || report.Success != 0 || report.RecoveryStatus != "success" || report.Results[1].FailureCode != FailureConnection {
		t.Fatalf("部分目标失败必须恢复已推送目标：%+v，%v", report, err)
	}
	assertVerifiedLocks(t, client, first, false)
}

func TestVerifiedDeploymentRequiresVerifierAndDoesNotHideCleanupFailure(t *testing.T) {
	client, server := newMemoryClient(), testServer("cleanup")
	original := seedVerifiedFiles(client, server)
	service := NewService(fixedClientFactory{client})
	report, err := service.DeployVerified(t.Context(), []Target{{Server: server}}, testArtifact(t), Verification{})
	if err == nil || report.Status != "failed" || len(client.commands) != 0 {
		t.Fatal("缺少校验器不得推送")
	}
	assertVerifiedFiles(t, client, original)
	client.removeError = ".scert-backup-"
	report, err = service.DeployVerified(t.Context(), []Target{{Server: server}}, testArtifact(t), Verification{Verify: func(context.Context) error { return nil }})
	if err == nil || report.Status != "failed" || report.VerificationStatus != "success" || report.Results[0].FailureCode != FailureCleanup {
		t.Fatalf("校验成功不能掩盖清理失败：%+v，%v", report, err)
	}
	assertVerifiedLocks(t, client, server, true)
}
