package httpapi

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type localRecoveryExecutor interface {
	PrepareRecoveryConfirmation(context.Context, string, string, deployment.RecoveryAction, func(context.Context, string) (deployment.Server, error)) (deployment.RecoveryConfirmation, string, error)
	ExecuteRecoveryDisposition(context.Context, deployment.RecoveryConfirmation, string, string, func(context.Context, string) (deployment.Server, error)) (deployment.RecoveryDisposition, error)
	GetRecoveryDisposition(context.Context, string) (deployment.RecoveryDispositionSummary, error)
}

func (r *localResetSession) DispositionAvailable() bool {
	_, ok := r.server.recoveryInspection.(localRecoveryExecutor)
	return ok && r.RecoveryAvailable()
}

func (r *localResetSession) recoveryServer(ctx context.Context, id string) (deployment.Server, error) {
	if !r.current(ctx) {
		return deployment.Server{}, localreset.ErrUnavailable
	}
	return r.server.servers.GetServerByID(ctx, id)
}

func (r *localResetSession) PrepareRecovery(ctx context.Context, id string, action deployment.RecoveryAction) (localreset.RecoveryPreparation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 新准备替换旧准备；任何失败也不能继续使用上次的确认对象。
	r.recoveryConfirmation = deployment.RecoveryConfirmation{}
	executor, ok := r.server.recoveryInspection.(localRecoveryExecutor)
	if !r.enterRecovery(ctx) || !ok {
		return localreset.RecoveryPreparation{}, localreset.ErrProtocol
	}
	confirmation, token, err := executor.PrepareRecoveryConfirmation(ctx, id, r.peer.Identity, action, r.recoveryServer)
	if err != nil || !r.current(ctx) {
		r.stage = -1
		return localreset.RecoveryPreparation{}, localreset.ErrUnavailable
	}
	r.recoveryConfirmation = confirmation
	return localreset.RecoveryPreparation{DispositionID: confirmation.DispositionID(), Token: token, Summary: confirmation.Summary()}, nil
}

func (r *localResetSession) ExecuteRecovery(ctx context.Context, id, token string) (deployment.RecoveryDispositionSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	confirmation := r.recoveryConfirmation
	r.recoveryConfirmation = deployment.RecoveryConfirmation{}
	executor, ok := r.server.recoveryInspection.(localRecoveryExecutor)
	if !r.enterRecovery(ctx) || !ok || id == "" || id != confirmation.DispositionID() {
		r.stage = -1
		return deployment.RecoveryDispositionSummary{}, localreset.ErrProtocol
	}
	item, err := executor.ExecuteRecoveryDisposition(ctx, confirmation, r.peer.Identity, token, r.recoveryServer)
	if !r.current(ctx) {
		r.stage = -1
		return deployment.RecoveryDispositionSummary{}, localreset.ErrUnavailable
	}
	// 执行失败但意图已落库时，返回明确的拒绝、不确定或运行中状态；不将错误伪装成成功，也不自动重试。
	summary := item.Summary()
	if summary.ID != id || summary.Validate() != nil {
		r.stage = -1
		return deployment.RecoveryDispositionSummary{}, localreset.ErrUnavailable
	}
	if err != nil && summary.Status == "succeeded" {
		r.stage = -1
		return deployment.RecoveryDispositionSummary{}, localreset.ErrUnavailable
	}
	return summary, nil
}

func (r *localResetSession) GetRecoveryDisposition(ctx context.Context, id string) (deployment.RecoveryDispositionSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	executor, ok := r.server.recoveryInspection.(localRecoveryExecutor)
	if !r.enterRecovery(ctx) || !ok {
		return deployment.RecoveryDispositionSummary{}, localreset.ErrProtocol
	}
	result, err := executor.GetRecoveryDisposition(ctx, id)
	if err != nil || !r.current(ctx) || result.ID != id || result.Validate() != nil {
		r.stage = -1
		return deployment.RecoveryDispositionSummary{}, localreset.ErrUnavailable
	}
	return result, nil
}

var _ localreset.RecoveryDispositionSession = (*localResetSession)(nil)

type localRecoveryReconciler interface {
	PrepareRecoveryReconciliation(context.Context, string, string, func(context.Context, string) (deployment.Server, error)) (deployment.RecoveryConfirmation, string, error)
}

func (r *localResetSession) PrepareRecoveryReconciliation(ctx context.Context, sourceID string) (localreset.RecoveryPreparation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoveryConfirmation = deployment.RecoveryConfirmation{}
	executor, ok := r.server.recoveryInspection.(localRecoveryReconciler)
	if !r.enterRecovery(ctx) || !ok || !r.DispositionAvailable() {
		return localreset.RecoveryPreparation{}, localreset.ErrProtocol
	}
	confirmation, token, err := executor.PrepareRecoveryReconciliation(ctx, sourceID, r.peer.Identity, r.recoveryServer)
	if err != nil || !r.current(ctx) || confirmation.Summary().SourceDispositionID != sourceID {
		r.stage = -1
		return localreset.RecoveryPreparation{}, localreset.ErrUnavailable
	}
	r.recoveryConfirmation = confirmation
	return localreset.RecoveryPreparation{DispositionID: confirmation.DispositionID(), Token: token, Summary: confirmation.Summary()}, nil
}

var _ localreset.RecoveryReconciliationSession = (*localResetSession)(nil)
