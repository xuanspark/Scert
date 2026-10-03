package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// executorIntegrationClient 仅模拟远端文件；处置占用、审计和终态使用真实 SQLite。
// 未列出的远端方法故意保持不可用，避免测试意外放行额外写操作。
type executorIntegrationClient struct {
	deployment.Client
	files map[string]string
}

var _ deployment.EvidenceClient = (*executorIntegrationClient)(nil)

func (c *executorIntegrationClient) CreateOwnedLock(context.Context, string, string) error {
	return errors.New("人工处置不能创建新的部署锁")
}

func (c *executorIntegrationClient) Connect(context.Context, deployment.Server) (deployment.Client, error) {
	return c, nil
}
func (c *executorIntegrationClient) Close() error { return nil }
func (c *executorIntegrationClient) Exists(_ context.Context, path string) (bool, error) {
	_, exists := c.files[path]
	return exists, nil
}
func (c *executorIntegrationClient) FileDigest(_ context.Context, path string) (string, error) {
	value, exists := c.files[path]
	if !exists {
		return "", errors.New("测试文件不存在")
	}
	return executorDigest(value), nil
}
func (c *executorIntegrationClient) ReadLockOwner(_ context.Context, path string) (string, error) {
	return c.files[path+"/owner"], nil
}
func (c *executorIntegrationClient) Remove(_ context.Context, path string) error {
	delete(c.files, path)
	return nil
}
func (c *executorIntegrationClient) Rename(_ context.Context, from, to string) error {
	value, exists := c.files[from]
	if !exists {
		return errors.New("测试源文件不存在")
	}
	c.files[to] = value
	delete(c.files, from)
	return nil
}
func (c *executorIntegrationClient) Unlock(_ context.Context, path string) error {
	delete(c.files, path)
	delete(c.files, path+"/owner")
	return nil
}
func (c *executorIntegrationClient) Run(context.Context, []string) (deployment.CommandResult, error) {
	return deployment.CommandResult{}, nil
}

func executorDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func executorIntegrationFixture(t *testing.T) (*SQLDeploymentTransactionRepository, *sql.DB, *deployment.Service, *executorIntegrationClient, deployment.Server, deployment.TransactionRecord) {
	t.Helper()
	_, db := newTaskRepositoryTest(t)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSQLDeploymentTransactionRepository(db)
	return seedRecoveryExecutor(t, repo, db)
}

func seedRecoveryExecutor(t *testing.T, repo *SQLDeploymentTransactionRepository, db *sql.DB) (*SQLDeploymentTransactionRepository, *sql.DB, *deployment.Service, *executorIntegrationClient, deployment.Server, deployment.TransactionRecord) {
	t.Helper()
	server := deployment.Server{
		ID: "executor-server", Name: "executor-server", Address: "192.0.2.10", Port: 22, Username: "deploy",
		AuthMethod: deployment.SSHAuthPassword, PasswordRef: "secret://ssh/password/test", HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/cert.pem", CertificateChainPath: "/chain.pem", PrivateKeyPath: "/key.pem", WebServer: deployment.WebServerNginx,
	}.Normalize()
	record := deployment.TransactionRecord{
		ID: "executor-transaction", TaskID: "task", AttemptID: "attempt", AttemptNumber: 1, ServerID: server.ID, ServerName: server.Name,
		Paths:  []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath},
		Status: deployment.TransactionStatusRecoveryRequired, Phase: deployment.TransactionPhaseReloadAttempted, ReloadAttempted: true,
		Evidence: &deployment.RecoveryEvidence{
			Version: 1, LockToken: executorDigest("owner"), Address: server.Address, Port: server.Port, HostKeyFingerprint: server.HostKeyFingerprint,
			ValidationCommand: server.ValidationCommand, ReloadCommand: server.ReloadCommand, Files: map[string]string{}, TemporaryFiles: map[string]string{},
			Baseline: deployment.RecoveryBaseline{Endpoint: "https://example.com", Domains: []string{"example.com"}, PreviousFingerprint: "old", ExpectedFingerprint: "new"},
		},
	}
	client := &executorIntegrationClient{files: map[string]string{}}
	for _, path := range record.Paths {
		lock, backup, temporary := path+".scert-lock", path+".scert-backup-"+record.ID, path+".scert-tmp-"+record.ID
		record.LockPaths = append(record.LockPaths, lock)
		record.TemporaryPaths = append(record.TemporaryPaths, temporary)
		record.InstalledPaths = append(record.InstalledPaths, path)
		record.Backups = append(record.Backups, deployment.TransactionBackup{Original: path, Saved: backup, OriginalExists: true, OriginalDigest: executorDigest("old")})
		record.Evidence.Files[path], record.Evidence.TemporaryFiles[temporary] = executorDigest("new"), executorDigest("temporary")
		client.files[path], client.files[backup], client.files[temporary] = "new", "old", "temporary"
		client.files[lock], client.files[lock+"/owner"] = "", record.Evidence.LockToken
	}
	if err := repo.CreateDeploymentTransaction(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	service := deployment.NewServiceWithJournal(client, repo)
	service.VerifyRecoveryTarget = func(context.Context, deployment.Server, deployment.RecoveryBaseline, bool) error { return nil }
	return repo, db, service, client, server, record
}

func TestRecoveryExecutorSQLiteAtomicLifecycle(t *testing.T) {
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionRecord, deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			repo, db, service, client, server, record := executorIntegrationFixture(t)
			before, err := repo.ListPendingDeploymentTransactions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			filesBefore := maps.Clone(client.files)
			resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
			confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
			if err != nil {
				t.Fatal(err)
			}
			item, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
			if err != nil || item.Status != "succeeded" {
				t.Fatalf("execution failed: %+v %v", item, err)
			}
			recoveryCount(t, db, "recovery_dispositions", 1)
			recoveryCount(t, db, "recovery_events", 2)
			recoveryCount(t, db, "recovery_claims", 0)
			stored, err := repo.GetRecoveryDisposition(t.Context(), confirmation.DispositionID())
			if err != nil || !reflect.DeepEqual(stored, item) {
				t.Fatal("result query lost completed result", err)
			}
			view, err := service.GetRecoveryDisposition(t.Context(), confirmation.DispositionID())
			if err != nil || view.Validate() != nil || !reflect.DeepEqual(view, item.Summary()) {
				t.Fatal("safe query omitted or changed persisted result", err)
			}
			var status string
			if err := db.QueryRowContext(t.Context(), "SELECT status FROM deployment_transactions WHERE id = ?", record.ID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			switch action {
			case deployment.RecoveryActionRecord:
				after, err := repo.ListPendingDeploymentTransactions(t.Context())
				if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(filesBefore, client.files) {
					t.Fatal("record-only changed original state", err)
				}
			case deployment.RecoveryActionCommit:
				if status != deployment.TransactionStatusCommitted || client.files[record.Paths[0]] != "new" {
					t.Fatal("commit not atomic with audit")
				}
			case deployment.RecoveryActionRollback:
				if status != deployment.TransactionStatusRolledBack || client.files[record.Paths[0]] != "old" {
					t.Fatal("rollback not atomic with audit")
				}
			}
			if _, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve); !errors.Is(err, deployment.ErrRecoveryConfirmation) {
				t.Fatal("completed confirmation replayed")
			}
		})
	}
}

func TestRecoveryExecutorSQLiteAuditFailuresAndRestart(t *testing.T) {
	for _, stage := range []string{"started", "finished"} {
		t.Run(stage, func(t *testing.T) {
			repo, db, service, client, server, record := executorIntegrationFixture(t)
			resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
			confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", deployment.RecoveryActionCommit, resolve)
			if err != nil {
				t.Fatal(err)
			}
			query := "CREATE TRIGGER executor_audit_failure BEFORE INSERT ON recovery_events WHEN NEW.event_type = '" + stage + "' BEGIN SELECT RAISE(ABORT, '测试审计失败'); END"
			if _, err := db.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			filesBefore := maps.Clone(client.files)
			item, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
			if !errors.Is(err, deployment.ErrRecoveryDispositionUnavailable) || item.Status == "succeeded" {
				t.Fatal("audit failure reported success")
			}
			if stage == "started" {
				recoveryCount(t, db, "recovery_dispositions", 0)
				recoveryCount(t, db, "recovery_claims", 0)
				recoveryCount(t, db, "recovery_events", 0)
				if !reflect.DeepEqual(filesBefore, client.files) {
					t.Fatal("begin audit failure modified remote")
				}
				return
			}
			recoveryCount(t, db, "recovery_claims", 1)
			recoveryCount(t, db, "recovery_events", 1)
			var sequence int
			var name, path string
			if err := db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			repo = NewSQLDeploymentTransactionRepository(reopened)
			stored, err := repo.GetRecoveryDisposition(t.Context(), confirmation.DispositionID())
			if err != nil || stored.Status != "running" {
				t.Fatal("uncertain result lost after reopening", err)
			}
			pending, err := repo.ListPendingDeploymentTransactions(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Status != deployment.TransactionStatusRecoveryRequired || pending[0].Phase != deployment.TransactionPhaseCommitVerified {
				t.Fatal("failed finish altered original terminal state", err)
			}
			if strings.Contains(stored.Intent.RecordDigest, token) {
				t.Fatal("confirmation token persisted")
			}
			restarted := deployment.NewServiceWithJournal(nil, repo)
			view, err := restarted.GetRecoveryDisposition(t.Context(), confirmation.DispositionID())
			if err != nil || view.Status != "running" || view.ID != confirmation.DispositionID() {
				t.Fatal("query after process restart failed", err)
			}
			if err := restarted.RecoverPending(t.Context(), func(context.Context, string) (deployment.Server, error) {
				t.Error("restart re-executed occupied transaction")
				return deployment.Server{}, errors.New("unexpected resolver")
			}); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
				t.Fatal("restart did not honor durable claim", err)
			}
		})
	}
}
