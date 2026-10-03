package httpapi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type localDispositionJournal struct {
	deployment.TransactionJournal
	mu         sync.Mutex
	record     deployment.TransactionRecord
	item       deployment.RecoveryDisposition
	begins     int
	finishes   int
	queries    int
	failFinish bool
}

func (j *localDispositionJournal) ListPendingDeploymentTransactions(context.Context) ([]deployment.TransactionRecord, error) {
	return []deployment.TransactionRecord{j.record}, nil
}
func (j *localDispositionJournal) BeginRecoveryDisposition(_ context.Context, intent deployment.RecoveryDispositionIntent) (deployment.RecoveryDisposition, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.begins++
	if j.item.Intent.ID != "" {
		return j.item, false, nil
	}
	now := time.Now().UTC()
	j.item = deployment.RecoveryDisposition{Intent: intent, Status: "running", CreatedAt: now, UpdatedAt: now}
	return j.item, true, nil
}
func (j *localDispositionJournal) FinishRecoveryDisposition(_ context.Context, id, result string) (deployment.RecoveryDisposition, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.finishes++
	if j.failFinish {
		return deployment.RecoveryDisposition{}, errors.New("secret-audit-error")
	}
	j.item.Status, j.item.ResultCode, j.item.UpdatedAt = "succeeded", result, time.Now().UTC()
	if result == "rejected" || result == "uncertain" {
		j.item.Status = result
	}
	return j.item, nil
}
func (j *localDispositionJournal) GetRecoveryDisposition(context.Context, string) (deployment.RecoveryDisposition, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.queries++
	return j.item, nil
}
func (*localDispositionJournal) HasRecoveryClaim(context.Context, string) (bool, error) {
	return false, nil
}

type localDispositionRemote struct{ deployment.Client }

func (localDispositionRemote) Exists(context.Context, string) (bool, error) { return false, nil }
func (localDispositionRemote) Close() error                                 { return nil }
func (localDispositionRemote) CreateOwnedLock(context.Context, string, string) error {
	return errors.New("unexpected mutation")
}
func (localDispositionRemote) ReadLockOwner(context.Context, string) (string, error) {
	return "", errors.New("absent")
}
func (localDispositionRemote) FileDigest(context.Context, string) (string, error) {
	return "", errors.New("absent")
}

type localDispositionFactory struct{}

func (localDispositionFactory) Connect(context.Context, deployment.Server) (deployment.Client, error) {
	return localDispositionRemote{}, nil
}

type localDispositionServerRepo struct {
	repository.ServerRepository
	server deployment.Server
}

func (r localDispositionServerRepo) GetServerByID(context.Context, string) (deployment.Server, error) {
	return r.server, nil
}

func localDispositionFixture(t *testing.T) (*Server, *localResetSession, *localDispositionJournal) {
	t.Helper()
	server := newLocalResetTestServer(t)
	target := deployment.Server{ID: "server-1", Name: "server-1", Address: "192.0.2.1", Port: 22, Username: "deploy", AuthMethod: deployment.SSHAuthPassword,
		PasswordRef: "secret://ssh/password/test", HostKeyFingerprint: "SHA256:test", CertificatePath: "/cert.pem", CertificateChainPath: "/chain.pem", PrivateKeyPath: "/key.pem", WebServer: deployment.WebServerNginx}.Normalize()
	journal := &localDispositionJournal{record: deployment.TransactionRecord{ID: "tx-1", ServerID: target.ID, Phase: "locked", Status: deployment.TransactionStatusRecoveryRequired,
		Paths: []string{target.CertificatePath, target.CertificateChainPath, target.PrivateKeyPath}}}
	server.recoveryInspection = deployment.NewServiceWithJournal(localDispositionFactory{}, journal)
	server.servers = localDispositionServerRepo{server: target}
	session := localDispositionNewSession(t, server)
	return server, session, journal
}
func localDispositionNewSession(t *testing.T, server *Server) *localResetSession {
	t.Helper()
	session, err := server.localResetFactory(func(context.Context, string, string) error { t.Error("unexpected mail"); return nil })(t.Context(), localreset.Peer{Identity: "uid:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session.(*localResetSession)
}

func TestLocalDispositionRecordAndReconnectQuery(t *testing.T) {
	server, session, journal := localDispositionFixture(t)
	if !session.DispositionAvailable() {
		t.Fatal("missing capability")
	}
	prepared, err := session.PrepareRecovery(t.Context(), "tx-1", deployment.RecoveryActionRecord)
	if err != nil || len(prepared.Token) != 64 {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	result, err := session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token)
	if err != nil || result.Status != "succeeded" || result.ResultCode != "observed" {
		t.Fatalf("execute: %+v %v", result, err)
	}
	if journal.item.Intent.Operator != "uid:0" || journal.record.Status != deployment.TransactionStatusRecoveryRequired || journal.record.Evidence != nil {
		t.Fatal("identity or original transaction changed")
	}
	if _, err := session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token); err == nil {
		t.Fatal("confirmation replayed")
	}
	next := localDispositionNewSession(t, server)
	queried, err := next.GetRecoveryDisposition(t.Context(), prepared.DispositionID)
	if err != nil || queried != result {
		t.Fatalf("query: %+v %v", queried, err)
	}
	if journal.begins != 1 || journal.finishes != 1 || journal.queries != 1 {
		t.Fatal("query replayed execution")
	}
	if err := next.Select(t.Context(), "email"); err == nil {
		t.Fatal("query switched to password reset")
	}
}

func TestLocalDispositionInvalidSessionAndConfirmationNeverBegin(t *testing.T) {
	for _, mode := range []string{"expired", "cancelled", "changed-admin", "closed", "reset-stage", "wrong-id", "wrong-token", "changed-peer", "replaced-confirmation"} {
		t.Run(mode, func(t *testing.T) {
			server, session, journal := localDispositionFixture(t)
			prepared, err := session.PrepareRecovery(t.Context(), "tx-1", deployment.RecoveryActionRecord)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "expired":
				session.expires = time.Time{}
			case "cancelled":
				cancel()
			case "changed-admin":
				server.config.Admin.Email = "changed@example.test"
			case "closed":
				session.Close()
			case "reset-stage":
				session.stage = 1
			case "wrong-id":
				prepared.DispositionID = strings.Repeat("a", 64)
			case "wrong-token":
				prepared.Token = strings.Repeat("a", 64)
			case "changed-peer":
				session.peer.Identity = "uid:123"
			case "replaced-confirmation":
				if _, err := session.PrepareRecovery(ctx, "tx-1", deployment.RecoveryActionRecord); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := session.ExecuteRecovery(ctx, prepared.DispositionID, prepared.Token); err == nil {
				t.Fatal("invalid execution accepted")
			}
			if _, err := session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token); err == nil {
				t.Fatal("failed confirmation reusable")
			}
			if journal.begins != 0 || journal.finishes != 0 {
				t.Fatal("invalid request wrote intent")
			}
			if session.recoveryConfirmation.DispositionID() != "" {
				t.Fatal("confirmation retained")
			}
		})
	}
}

func TestLocalDispositionConcurrentExecuteOnce(t *testing.T) {
	_, session, journal := localDispositionFixture(t)
	prepared, err := session.PrepareRecovery(t.Context(), "tx-1", deployment.RecoveryActionRecord)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token)
		}()
	}
	wg.Wait()
	if journal.begins != 1 || journal.finishes != 1 {
		t.Fatal("concurrent execution duplicated")
	}
}

func TestLocalDispositionAuditFailureReturnsRunning(t *testing.T) {
	server, session, journal := localDispositionFixture(t)
	journal.failFinish = true
	prepared, err := session.PrepareRecovery(t.Context(), "tx-1", deployment.RecoveryActionRecord)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token)
	if err != nil || result.Status != "running" || result.ResultCode != "" {
		t.Fatalf("false success: %+v %v", result, err)
	}
	next := localDispositionNewSession(t, server)
	if result, err := next.GetRecoveryDisposition(t.Context(), prepared.DispositionID); err != nil || result.Status != "running" {
		t.Fatalf("query: %+v %v", result, err)
	}
}

func TestLocalDispositionMissingEvidenceAndReaderOnly(t *testing.T) {
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		_, session, journal := localDispositionFixture(t)
		if _, err := session.PrepareRecovery(t.Context(), "tx-1", action); err == nil {
			t.Fatal("missing evidence authorized mutation")
		}
		if journal.begins != 0 || session.recoveryConfirmation.DispositionID() != "" {
			t.Fatal("invalid confirmation retained")
		}
	}
	server := newLocalResetTestServer(t)
	session := newLocalRecoverySession(t, server, &testLocalRecoveryReader{})
	if session.DispositionAvailable() {
		t.Fatal("reader advertised writes")
	}
	if _, err := session.PrepareRecovery(t.Context(), "tx-1", deployment.RecoveryActionRecord); err == nil {
		t.Fatal("reader accepted action")
	}
}
