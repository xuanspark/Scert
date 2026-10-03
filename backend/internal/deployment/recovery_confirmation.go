package deployment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sync/atomic"
)

var ErrRecoveryConfirmation = errors.New("人工处置确认无效、已使用或已过期，请重新核查")

// RecoveryConfirmation 的副本共享消费状态，不能通过复制对象重复授权。
// 本机会话持有对象，终端仅收到短时令牌和脱敏摘要；令牌不写入数据库。
type RecoveryConfirmation struct {
	state *recoveryConfirmationState
}

type recoveryConfirmationState struct {
	review RecoveryReview
	token  [sha256.Size]byte
	intent RecoveryDispositionIntent
	used   atomic.Bool
}

func (c RecoveryConfirmation) Summary() RecoveryReviewSummary {
	if c.state == nil {
		return RecoveryReviewSummary{}
	}
	return c.state.review.Summary()
}

func (c RecoveryConfirmation) DispositionID() string {
	if c.state == nil {
		return ""
	}
	return c.state.intent.ID
}

func (s *Service) PrepareRecoveryConfirmation(ctx context.Context, id, operator string, action RecoveryAction, resolve func(context.Context, string) (Server, error)) (RecoveryConfirmation, string, error) {
	review, err := s.PrepareRecoveryReview(ctx, id, operator, action, resolve)
	if err != nil {
		return RecoveryConfirmation{}, "", err
	}
	return s.newRecoveryConfirmation(ctx, review)
}

func (s *Service) newRecoveryConfirmation(ctx context.Context, review RecoveryReview) (RecoveryConfirmation, string, error) {
	if s == nil || review.service != s {
		return RecoveryConfirmation{}, "", ErrRecoveryConfirmation
	}
	var entropy [96]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return RecoveryConfirmation{}, "", ErrRecoveryConfirmation
	}
	token := hex.EncodeToString(entropy[:32])
	intent, err := review.DispositionIntent(hex.EncodeToString(entropy[32:64]), hex.EncodeToString(entropy[64:]))
	if err != nil {
		return RecoveryConfirmation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return RecoveryConfirmation{}, "", err
	}
	return RecoveryConfirmation{state: &recoveryConfirmationState{
		review: review, intent: intent, token: sha256.Sum256([]byte(token)),
	}}, token, nil
}

// consumeRecoveryConfirmation 每个对象只允许一次确认尝试，错误身份、错误令牌和取消均不可重放。
// 成功消费仍不是远端写授权，执行器必须取得持久化占用并重新检查现场。
func (s *Service) consumeRecoveryConfirmation(ctx context.Context, c RecoveryConfirmation, operator, token string) (RecoveryReview, RecoveryDispositionIntent, error) {
	fail := func(err error) (RecoveryReview, RecoveryDispositionIntent, error) {
		return RecoveryReview{}, RecoveryDispositionIntent{}, err
	}
	state := c.state
	if state == nil || !state.used.CompareAndSwap(false, true) {
		return fail(ErrRecoveryConfirmation)
	}
	if s == nil || state.review.service != s || s.now == nil || !validReviewOperator(operator) || state.review.operator != operator ||
		!s.now().Before(state.review.summary.ExpiresAt) || len(token) != 64 {
		return fail(ErrRecoveryConfirmation)
	}
	digest := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(digest[:], state.token[:]) != 1 {
		return fail(ErrRecoveryConfirmation)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return state.review, state.intent, nil
}
