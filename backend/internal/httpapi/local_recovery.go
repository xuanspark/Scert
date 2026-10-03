package httpapi

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type localRecoveryReader interface {
	ListPendingRecoveries(context.Context, string) (deployment.PendingRecoveryPage, error)
	InspectPending(context.Context, string, func(context.Context, string) (deployment.Server, error)) (deployment.RecoveryInspection, error)
}

func (r *localResetSession) RecoveryAvailable() bool {
	return r.server.recoveryInspection != nil && r.server.servers != nil
}

// enterRecovery 保留会话有效期和管理员配置校验，进入核查后不能切换到密码重置。
func (r *localResetSession) enterRecovery(ctx context.Context) bool {
	if (r.stage != 0 && r.stage != 3) || !r.current(ctx) || !r.RecoveryAvailable() {
		r.stage = -1
		return false
	}
	r.stage = 3
	return true
}

func (r *localResetSession) ListRecoveries(ctx context.Context, cursor string) (deployment.PendingRecoveryPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enterRecovery(ctx) {
		return deployment.PendingRecoveryPage{}, localreset.ErrProtocol
	}
	page, err := r.server.recoveryInspection.ListPendingRecoveries(ctx, cursor)
	if err != nil || !r.current(ctx) {
		r.stage = -1
		return deployment.PendingRecoveryPage{}, localreset.ErrUnavailable
	}
	return page, nil
}

func (r *localResetSession) InspectRecovery(ctx context.Context, id string) (deployment.RecoveryInspection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.enterRecovery(ctx) {
		return deployment.RecoveryInspection{}, localreset.ErrProtocol
	}
	result, err := r.server.recoveryInspection.InspectPending(ctx, id, r.server.servers.GetServerByID)
	if err != nil || !r.current(ctx) {
		r.stage = -1
		return deployment.RecoveryInspection{}, localreset.ErrUnavailable
	}
	return result, nil
}
