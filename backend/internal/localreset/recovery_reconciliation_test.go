package localreset

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type reconciliationSession struct{ dispositionTestSession }

func (s *reconciliationSession) PrepareRecoveryReconciliation(ctx context.Context, sourceID string) (RecoveryPreparation, error) {
	value, err := s.PrepareRecovery(ctx, "tx-1", deployment.RecoveryActionVerifyCommit)
	value.Summary.SourceDispositionID = sourceID
	value.Summary.Phase = deployment.TransactionPhaseCommitVerified
	s.prepared = value
	return value, err
}

func (s *reconciliationSession) ExecuteRecovery(ctx context.Context, id, token string) (deployment.RecoveryDispositionSummary, error) {
	sourceID := s.prepared.Summary.SourceDispositionID
	value, err := s.dispositionTestSession.ExecuteRecovery(ctx, id, token)
	if err != nil {
		return value, err
	}
	value.SourceDispositionID, value.ResultCode = sourceID, deployment.RecoveryResultCommitted
	s.result = value
	return value, nil
}

func TestRecoveryReconciliationProtocolAndQuery(t *testing.T) {
	session := &reconciliationSession{}
	_, conn, done := recoveryConnection(t, session)
	client, err := Begin(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := strings.Repeat("a", 64)
	prepared, err := client.PrepareRecoveryReconciliation(sourceID)
	if err != nil || prepared.Summary.SourceDispositionID != sourceID {
		t.Fatal("原处置绑定丢失", err)
	}
	result, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token)
	if err != nil || result.SourceDispositionID != sourceID || result.Status != "succeeded" {
		t.Fatal("重新核查结果无效", err)
	}
	queried, err := client.GetRecoveryDisposition(result.ID)
	if err != nil || queried != result {
		t.Fatal("查询改变了关联结果", err)
	}
	if _, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token); err == nil {
		t.Fatal("旧令牌被重放")
	}
	_ = client.Close()
	<-done
	if session.prepares.Load() != 1 || session.executes.Load() != 2 || session.queries.Load() != 1 || session.resets.Load() != 0 {
		t.Fatal("调用了错误业务入口")
	}
}

func TestRecoveryReconciliationRejectsUnboundResponse(t *testing.T) {
	sourceID := strings.Repeat("a", 64)
	session := &reconciliationSession{}
	prepared, err := session.PrepareRecoveryReconciliation(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Action: "recovery_reconcile_prepare", DispositionID: sourceID}
	for _, mode := range []string{"source", "action", "same-id", "transaction", "mixed", "expired-range"} {
		t.Run(mode, func(t *testing.T) {
			value := prepared
			response := Response{Preparation: &value}
			switch mode {
			case "source":
				value.Summary.SourceDispositionID = strings.Repeat("d", 64)
			case "action":
				value.Summary.Action = deployment.RecoveryActionCommit
			case "same-id":
				value.DispositionID = sourceID
			case "transaction":
				value.Summary.TransactionID = "../bad"
			case "mixed":
				response.Disposition = &deployment.RecoveryDispositionSummary{}
			case "expired-range":
				value.Summary.ExpiresAt = value.Summary.ObservedAt.Add(2 * time.Minute)
			}
			if validDispositionResponse(request, response) {
				t.Fatal("接受了混合或无绑定准备响应")
			}
		})
	}
	if validRecoveryAction(deployment.RecoveryActionVerifyCommit) || validRecoveryAction(deployment.RecoveryActionVerifyRollback) {
		t.Fatal("普通准备允许绕过原处置绑定")
	}
}

func TestRecoveryReconciliationOldServerFailsClosed(t *testing.T) {
	session := &dispositionTestSession{}
	_, conn, done := recoveryConnection(t, session)
	client, err := Begin(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PrepareRecoveryReconciliation(strings.Repeat("a", 64)); err == nil {
		t.Fatal("旧服务错误地支持了重新核查")
	}
	_ = client.Close()
	<-done
	if session.prepares.Load() != 0 || session.executes.Load() != 0 {
		t.Fatal("旧服务错误地执行了原动作")
	}
}
