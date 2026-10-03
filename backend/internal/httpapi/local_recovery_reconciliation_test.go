package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type localReconciliationJournal struct {
	localDispositionJournal
	source deployment.RecoveryDisposition
}

func (j *localReconciliationJournal) GetRecoveryDisposition(ctx context.Context, id string) (deployment.RecoveryDisposition, error) {
	if id == j.source.Intent.ID { return j.source, nil }
	return j.localDispositionJournal.GetRecoveryDisposition(ctx, id)
}

func localReconciliationFixture(t *testing.T) (*Server, *localResetSession, *localReconciliationJournal) {
	t.Helper()
	server, _, original := localDispositionFixture(t)
	target := server.servers.(localDispositionServerRepo).server
	journal := &localReconciliationJournal{}
	journal.record = original.record
	journal.record.Phase = deployment.TransactionPhaseCommitVerified
	journal.record.Evidence = &deployment.RecoveryEvidence{Version: 1, LockToken: strings.Repeat("d", 64), Address: target.Address,
		Port: target.Port, HostKeyFingerprint: target.HostKeyFingerprint, ValidationCommand: target.ValidationCommand, ReloadCommand: target.ReloadCommand,
		Files: map[string]string{}, TemporaryFiles: map[string]string{}, Baseline: deployment.RecoveryBaseline{Endpoint: "https://example.test", Domains: []string{"example.test"}, ExpectedFingerprint: "new"}}
	for _, path := range journal.record.Paths {
		journal.record.LockPaths = append(journal.record.LockPaths, path+".scert-lock")
		journal.record.Backups = append(journal.record.Backups, deployment.TransactionBackup{Original: path, Saved: path+".scert-backup-"+journal.record.ID})
		journal.record.Evidence.Files[path] = strings.Repeat("e", 64)
	}
	service := deployment.NewServiceWithJournal(localDispositionFactory{}, journal)
	service.VerifyRecoveryTarget = func(context.Context, deployment.Server, deployment.RecoveryBaseline, bool) error { return nil }
	resolve := func(context.Context, string) (deployment.Server, error) { return target, nil }
	review, err := service.PrepareRecoveryReview(t.Context(), journal.record.ID, "uid:0", deployment.RecoveryActionCommit, resolve)
	if err != nil { t.Fatal(err) }
	intent, err := review.DispositionIntent(strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil { t.Fatal(err) }
	now := time.Now().UTC()
	journal.source = deployment.RecoveryDisposition{Intent: intent, Status: "uncertain", ResultCode: "uncertain", CreatedAt: now, UpdatedAt: now}
	server.recoveryInspection = service
	return server, localDispositionNewSession(t, server), journal
}

func TestLocalReconciliationRejectsInvalidSession(t *testing.T) {
	for _, mode := range []string{"expired", "cancelled", "changed-admin", "closed", "reset-stage", "wrong-id", "wrong-token", "changed-peer", "foreign-session", "replaced-confirmation"} {
		t.Run(mode, func(t *testing.T) {
			server, session, journal := localReconciliationFixture(t)
			prepared, err := session.PrepareRecoveryReconciliation(t.Context(), journal.source.Intent.ID)
			if err != nil { t.Fatal(err) }
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "expired": session.expires = time.Time{}
			case "cancelled": cancel()
			case "changed-admin": server.config.Admin.Email = "changed@example.test"
			case "closed": session.Close()
			case "reset-stage": session.stage = 1
			case "wrong-id": prepared.DispositionID = journal.source.Intent.ID
			case "wrong-token": prepared.Token = strings.Repeat("a", 64)
			case "changed-peer": session.peer.Identity = "uid:123"
			case "foreign-session": session = localDispositionNewSession(t, server)
			case "replaced-confirmation":
				if _, err := session.PrepareRecoveryReconciliation(ctx, journal.source.Intent.ID); err != nil { t.Fatal(err) }
			}
			if _, err := session.ExecuteRecovery(ctx, prepared.DispositionID, prepared.Token); err == nil { t.Fatal("无效会话执行了重新核查") }
			if _, err := session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token); err == nil { t.Fatal("失败确认被重放") }
			if journal.begins != 0 || journal.finishes != 0 || session.recoveryConfirmation.DispositionID() != "" { t.Fatal("无效会话写入了审计或保留了确认") }
		})
	}
}

func TestLocalReconciliationRejectsMissingFilesAndQueriesNewAudit(t *testing.T) {
	server, session, journal := localReconciliationFixture(t)
	prepared, err := session.PrepareRecoveryReconciliation(t.Context(), journal.source.Intent.ID)
	if err != nil { t.Fatal(err) }
	result, err := session.ExecuteRecovery(t.Context(), prepared.DispositionID, prepared.Token)
	if err != nil || result.Status != "rejected" || result.SourceDispositionID != journal.source.Intent.ID { t.Fatalf("缺失文件仍被放行：%+v %v", result, err) }
	if journal.item.Intent.Operator != "uid:0" || journal.begins != 1 || journal.finishes != 1 || journal.source.Status != "uncertain" { t.Fatal("身份或历史记录被改变") }
	next := localDispositionNewSession(t, server)
	queried, err := next.GetRecoveryDisposition(t.Context(), result.ID)
	if err != nil || queried != result { t.Fatal("重连未查到关联审计", err) }
}
