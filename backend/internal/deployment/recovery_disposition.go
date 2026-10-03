package deployment

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrRecoveryDispositionInvalid     = errors.New("人工处置记录参数无效")
	ErrRecoveryDispositionConflict    = errors.New("人工处置请求与已有记录不一致")
	ErrRecoveryDispositionBusy        = errors.New("该事务已有未释放的人工处置占用")
	ErrRecoveryDispositionUnavailable = errors.New("人工处置记录暂不可用，未授权执行远端动作")
)

// RecoveryDispositionIntent 仅保存绑定摘要；不能包含历史证据、凭据或确认令牌原文。
type RecoveryDispositionIntent struct {
	SourceDispositionID string
	ID                  string
	TransactionID       string
	ServerID            string
	Operator            string
	Action              RecoveryAction
	RequestKey          string
	RecordDigest        string
	ServerDigest        string
	ObservationDigest   string
}

type RecoveryDisposition struct {
	Intent     RecoveryDispositionIntent `json:"-"`
	Status     string                    `json:"status"`
	ResultCode string                    `json:"result_code"`
	CreatedAt  time.Time                 `json:"created_at"`
	UpdatedAt  time.Time                 `json:"updated_at"`
}

const (
	RecoveryResultObserved   = "observed"
	RecoveryResultCommitted  = "committed"
	RecoveryResultRolledBack = "rolled_back"
	RecoveryResultRejected   = "rejected"
	RecoveryResultUncertain  = "uncertain"
)

// RecoveryDispositionJournal 的开始成功且新建返回值为真，才允许执行器继续核验。
// 记录本身不是远端写入授权；执行器仍需一次性确认、完整互斥及动作专属核验。
type RecoveryDispositionJournal interface {
	BeginRecoveryDisposition(context.Context, RecoveryDispositionIntent) (RecoveryDisposition, bool, error)
	FinishRecoveryDisposition(context.Context, string, string) (RecoveryDisposition, error)
	GetRecoveryDisposition(context.Context, string) (RecoveryDisposition, error)
	HasRecoveryClaim(context.Context, string) (bool, error)
}

func (r RecoveryReview) DispositionIntent(id, requestKey string) (RecoveryDispositionIntent, error) {
	if r.service == nil || r.service.now == nil || !r.service.now().Before(r.summary.ExpiresAt) {
		return RecoveryDispositionIntent{}, ErrRecoveryReviewExpired
	}
	intent := RecoveryDispositionIntent{
		SourceDispositionID: r.summary.SourceDispositionID,
		ID:                  id, TransactionID: r.summary.TransactionID, ServerID: r.summary.ServerID,
		Operator: r.operator, Action: r.summary.Action, RequestKey: requestKey,
		RecordDigest:      hex.EncodeToString(r.recordDigest[:]),
		ServerDigest:      hex.EncodeToString(r.serverDigest[:]),
		ObservationDigest: hex.EncodeToString(r.observationDigest[:]),
	}
	return intent, intent.Validate()
}

func (i RecoveryDispositionIntent) Validate() error {
	if isRecoveryVerification(i.Action) != (i.SourceDispositionID != "") || (i.SourceDispositionID != "" && (!validDispositionID(i.SourceDispositionID) || i.SourceDispositionID == i.ID)) {
		return ErrRecoveryDispositionInvalid
	}
	if !validInspectionID(i.TransactionID) || !validRecoverySummary(i.ServerID) ||
		!validReviewOperator(i.Operator) || !validRecoveryAction(i.Action) {
		return ErrRecoveryDispositionInvalid
	}
	for _, value := range []string{i.ID, i.RequestKey, i.RecordDigest, i.ServerDigest, i.ObservationDigest} {
		if len(value) != 64 || value != strings.ToLower(value) || value == strings.Repeat("0", 64) {
			return ErrRecoveryDispositionInvalid
		}
		if _, err := hex.DecodeString(value); err != nil {
			return ErrRecoveryDispositionInvalid
		}
	}
	return nil
}

// SameRequest 忽略重试时新生成的记录标识，但必须匹配原请求的全部绑定值。
func (i RecoveryDispositionIntent) SameRequest(other RecoveryDispositionIntent) bool {
	i.ID, other.ID = "", ""
	return i == other
}

func (i RecoveryDispositionIntent) MatchesRecord(record TransactionRecord) bool {
	if record.ID != i.TransactionID || record.ServerID != i.ServerID ||
		(record.Status != TransactionStatusPending && record.Status != TransactionStatusRecoveryRequired) ||
		(i.Action != RecoveryActionRecord && record.Evidence == nil) {
		return false
	}
	digest, err := reviewDigest(record)
	return err == nil && hex.EncodeToString(digest[:]) == i.RecordDigest
}

func (i RecoveryDispositionIntent) AllowsResult(result string) bool {
	switch result {
	case RecoveryResultRejected, RecoveryResultUncertain:
		return validRecoveryAction(i.Action)
	case RecoveryResultObserved:
		return i.Action == RecoveryActionRecord
	case RecoveryResultCommitted:
		return i.Action == RecoveryActionCommit || i.Action == RecoveryActionVerifyCommit
	case RecoveryResultRolledBack:
		return i.Action == RecoveryActionRollback || i.Action == RecoveryActionVerifyRollback
	}
	return false
}
