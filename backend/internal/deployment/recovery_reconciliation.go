package deployment

import (
	"context"
	"encoding/hex"
	"time"
)

func isRecoveryVerification(action RecoveryAction) bool {
	return action == RecoveryActionVerifyCommit || action == RecoveryActionVerifyRollback
}

// Reconciles 仅允许已持久化结束的不确定原动作；不接管运行中处置或再次执行文件动作。
func (i RecoveryDispositionIntent) Reconciles(source RecoveryDisposition) bool {
	if i.SourceDispositionID == "" || i.SourceDispositionID != source.Intent.ID || source.Intent.Validate() != nil ||
		source.Status != RecoveryResultUncertain || source.ResultCode != RecoveryResultUncertain ||
		i.TransactionID != source.Intent.TransactionID || i.ServerID != source.Intent.ServerID || i.ServerDigest != source.Intent.ServerDigest {
		return false
	}
	return (i.Action == RecoveryActionVerifyCommit && source.Intent.Action == RecoveryActionCommit) ||
		(i.Action == RecoveryActionVerifyRollback && source.Intent.Action == RecoveryActionRollback)
}

// PrepareRecoveryReconciliation 只供受保护本机通道调用，必须明确指定原处置编号并重新确认。
func (s *Service) PrepareRecoveryReconciliation(ctx context.Context, sourceID, operator string, resolve func(context.Context, string) (Server, error)) (RecoveryConfirmation, string, error) {
	review, err := s.prepareRecoveryReconciliation(ctx, sourceID, operator, resolve)
	if err != nil {
		return RecoveryConfirmation{}, "", err
	}
	return s.newRecoveryConfirmation(ctx, review)
}

func (s *Service) prepareRecoveryReconciliation(ctx context.Context, sourceID, operator string, resolve func(context.Context, string) (Server, error)) (RecoveryReview, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s == nil || !validDispositionID(sourceID) || !validReviewOperator(operator) {
		return RecoveryReview{}, ErrRecoveryReviewInvalid
	}
	journal, ok := s.journal.(RecoveryDispositionJournal)
	if !ok {
		return RecoveryReview{}, ErrRecoveryDispositionUnavailable
	}
	source, err := journal.GetRecoveryDisposition(ctx, sourceID)
	if err != nil || source.Intent.ID != sourceID || source.Intent.Validate() != nil || source.Summary().Validate() != nil ||
		source.Status != RecoveryResultUncertain || source.ResultCode != RecoveryResultUncertain {
		return RecoveryReview{}, ErrRecoveryActionRejected
	}
	var action RecoveryAction
	switch source.Intent.Action {
	case RecoveryActionCommit:
		action = RecoveryActionVerifyCommit
	case RecoveryActionRollback:
		action = RecoveryActionVerifyRollback
	default:
		return RecoveryReview{}, ErrRecoveryActionRejected
	}
	review, err := s.prepareRecoveryReview(ctx, source.Intent.TransactionID, operator, action, resolve)
	if err != nil {
		return RecoveryReview{}, err
	}
	review.summary.SourceDispositionID = sourceID
	intent := RecoveryDispositionIntent{SourceDispositionID: sourceID, TransactionID: review.summary.TransactionID,
		ServerID: review.summary.ServerID, ServerDigest: hex.EncodeToString(review.serverDigest[:]), Action: action}
	if !intent.Reconciles(source) || !verificationPhase(review.summary.Phase, action) || s.VerifyRecoveryTarget == nil {
		return RecoveryReview{}, ErrRecoveryActionRejected
	}
	return review, nil
}

func verificationPhase(phase string, action RecoveryAction) bool {
	return (action == RecoveryActionVerifyCommit && phase == TransactionPhaseCommitVerified) ||
		(action == RecoveryActionVerifyRollback && phase == TransactionPhaseRollbackVerified)
}

// validateRecoveryCompletion 不删除任何远端文件，残留锁或备份均要求继续保留占用。
func validateRecoveryCompletion(ctx context.Context, client Client, record TransactionRecord, committed bool) error {
	for _, lock := range record.LockPaths {
		if exists, err := client.Exists(ctx, lock); err != nil || exists {
			return ErrRecoveryActionRejected
		}
	}
	for _, backup := range record.Backups {
		if committed {
			if err := checkFileDigest(ctx, client, backup.Original, record.Evidence.Files[backup.Original]); err != nil {
				return err
			}
		} else if backup.OriginalExists {
			if err := checkFileDigest(ctx, client, backup.Original, backup.OriginalDigest); err != nil {
				return err
			}
		} else if exists, err := client.Exists(ctx, backup.Original); err != nil || exists {
			return ErrRecoveryActionRejected
		}
		for _, path := range []string{backup.Saved, backup.Original + ".scert-tmp-" + record.ID} {
			if exists, err := client.Exists(ctx, path); err != nil || exists {
				return ErrRecoveryActionRejected
			}
		}
	}
	return ctx.Err()
}

func (s *Service) verifyRecoveryCompletion(ctx context.Context, record TransactionRecord, server Server, client Client, action RecoveryAction) error {
	committed := action == RecoveryActionVerifyCommit
	if err := validateRecoveryCompletion(ctx, client, record, committed); err != nil {
		return err
	}
	if err := s.VerifyRecoveryTarget(ctx, server, record.Evidence.Baseline, committed); err != nil {
		return err
	}
	// HTTPS 检查结束后重新读取现场，避免检查期间发生的文件变化被忽略。
	return validateRecoveryCompletion(ctx, client, record, committed)
}
