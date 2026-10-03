package deployment

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRecoveryActionRejected  = errors.New("人工处置前置核验未通过，未执行远端变更")
	ErrRecoveryActionUncertain = errors.New("人工处置结果不确定，已保留占用，请查询记录并核查现场")
)

// ExecuteRecoveryDisposition 仅供受保护本机会话使用，操作者必须来自本机对端身份。
// 一次性确认、共同操作门、持久化占用和远端锁各自独立，缺一不可。
func (s *Service) ExecuteRecoveryDisposition(ctx context.Context, confirmation RecoveryConfirmation, operator, token string, resolve func(context.Context, string) (Server, error)) (RecoveryDisposition, error) {
	review, intent, err := s.consumeRecoveryConfirmation(ctx, confirmation, operator, token)
	if err != nil {
		return RecoveryDisposition{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	release, err := s.acquireOperation(ctx)
	if err != nil {
		return RecoveryDisposition{}, err
	}
	defer release()
	journal, ok := s.journal.(RecoveryDispositionJournal)
	if !ok || resolve == nil {
		return RecoveryDisposition{}, ErrRecoveryDispositionUnavailable
	}
	if !s.now().Before(review.summary.ExpiresAt) {
		return RecoveryDisposition{}, ErrRecoveryConfirmation
	}
	item, created, err := journal.BeginRecoveryDisposition(ctx, intent)
	if err != nil {
		return RecoveryDisposition{}, ErrRecoveryDispositionUnavailable
	}
	if !created {
		// 响应丢失后的既有请求只返回持久结果，绝不重新连接或执行。
		return item, nil
	}
	finish := func(result string, actionErr error) (RecoveryDisposition, error) {
		// 请求取消不能跳过结束审计；保存失败保留原占用且绝不返回成功。
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer finishCancel()
		finished, finishErr := journal.FinishRecoveryDisposition(finishCtx, intent.ID, result)
		if finishErr != nil {
			return item, ErrRecoveryDispositionUnavailable
		}
		return finished, actionErr
	}
	if err := s.RecheckRecoveryReview(ctx, review, operator, resolve); err != nil {
		return finish(RecoveryResultRejected, ErrRecoveryActionRejected)
	}
	if review.summary.Action == RecoveryActionRecord {
		return finish(RecoveryResultObserved, nil)
	}
	result, actionErr := s.executeRecoveryAction(ctx, review, resolve)
	return finish(result, actionErr)
}

func (s *Service) executeRecoveryAction(ctx context.Context, review RecoveryReview, resolve func(context.Context, string) (Server, error)) (result string, actionErr error) {
	result, actionErr = RecoveryResultRejected, ErrRecoveryActionRejected
	record, recordDigest, serverDigest, err := s.reviewRecord(ctx, review.summary.TransactionID, resolve)
	if err != nil || recordDigest != review.recordDigest || serverDigest != review.serverDigest {
		return
	}
	server, err := resolve(ctx, record.ServerID)
	if err != nil {
		return
	}
	server = server.Normalize()
	currentServerDigest, err := reviewServerDigest(server)
	if err != nil || currentServerDigest != review.serverDigest || validateRecoveryAction(record, review.summary.Action, s.VerifyRecoveryTarget != nil) != nil {
		return
	}
	client, err := s.factory.Connect(ctx, server)
	if err != nil {
		return
	}
	mutated := false
	defer func() {
		// 在结果审计和释放共同操作门之前关闭连接，关闭失败不能伪装成成功。
		if closeErr := client.Close(); closeErr != nil {
			if mutated {
				result, actionErr = RecoveryResultUncertain, ErrRecoveryActionUncertain
			} else {
				result, actionErr = RecoveryResultRejected, ErrRecoveryActionRejected
			}
		}
	}()
	if err := ctx.Err(); err != nil || !s.now().Before(review.summary.ExpiresAt) {
		return
	}
	switch review.summary.Action {
	case RecoveryActionCommit:
		err = s.executeRecoveryCommit(ctx, &record, server, client, &mutated)
		result = RecoveryResultCommitted
	case RecoveryActionRollback:
		err = s.executeRecoveryRollback(ctx, &record, server, client, &mutated)
		result = RecoveryResultRolledBack
	default:
		return
	}
	if err == nil {
		return result, nil
	}
	if mutated {
		return RecoveryResultUncertain, ErrRecoveryActionUncertain
	}
	return RecoveryResultRejected, ErrRecoveryActionRejected
}

func validateRecoveryAction(record TransactionRecord, action RecoveryAction, verifier bool) error {
	if record.Evidence == nil || !verifier || len(record.Backups) != len(record.Paths) {
		return ErrRecoveryActionRejected
	}
	switch action {
	case RecoveryActionCommit:
		if record.Evidence.Baseline.ExpectedFingerprint == "" {
			return ErrRecoveryActionRejected
		}
		switch record.Phase {
		case TransactionPhaseReloadAttempted, TransactionPhaseCommitting, TransactionPhaseCommitVerified:
			return nil
		}
	case RecoveryActionRollback:
		if record.Evidence.Baseline.PreviousFingerprint == "" {
			return ErrRecoveryActionRejected
		}
		switch record.Phase {
		case TransactionPhaseLocked, TransactionPhaseUploaded, TransactionPhaseReplacing, TransactionPhaseInstalled,
			TransactionPhaseReloadAttempted, TransactionPhaseCommitting, TransactionPhaseRollbackVerified:
			return nil
		}
	}
	return ErrRecoveryActionRejected
}
