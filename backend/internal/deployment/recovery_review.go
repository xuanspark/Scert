package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

type RecoveryAction string

const (
	RecoveryActionRecord         RecoveryAction = "record_inspection"
	RecoveryActionCommit         RecoveryAction = "finish_commit"
	RecoveryActionRollback       RecoveryAction = "restore_baseline"
	RecoveryActionVerifyCommit   RecoveryAction = "verify_commit"
	RecoveryActionVerifyRollback RecoveryAction = "verify_rollback"
	RecoveryReviewLifetime                      = time.Minute
)

var (
	ErrRecoveryReviewInvalid  = errors.New("人工处置准备参数无效")
	ErrRecoveryReviewExpired  = errors.New("人工处置准备已过期，请重新核查")
	ErrRecoveryReviewChanged  = errors.New("事务、服务器或现场已变化，请重新准备处置")
	ErrRecoveryReviewEvidence = errors.New("历史恢复证据不足，只能记录核查结论，不能提交或回滚")
)

type RecoveryReviewSummary struct {
	SourceDispositionID string         `json:"source_disposition_id,omitempty"`
	TransactionID       string         `json:"transaction_id"`
	ServerID            string         `json:"server_id"`
	Phase               string         `json:"phase"`
	Action              RecoveryAction `json:"action"`
	HistoricalEvidence  bool           `json:"historical_evidence"`
	ObservedAt          time.Time      `json:"observed_at"`
	ExpiresAt           time.Time      `json:"expires_at"`
}

// Validate 与服务端历史记录使用同一摘要边界，允许旧记录没有阶段，但不允许控制字符或超长字段。
func (r RecoveryReviewSummary) Validate() error {
	if !validInspectionID(r.TransactionID) || !validRecoverySummary(r.ServerID) ||
		(r.Phase != "" && !validRecoverySummary(r.Phase)) || !validRecoveryAction(r.Action) ||
		r.ObservedAt.IsZero() || !r.ExpiresAt.After(r.ObservedAt) || r.ExpiresAt.Sub(r.ObservedAt) > RecoveryReviewLifetime ||
		(r.Action != RecoveryActionRecord && !r.HistoricalEvidence) {
		return ErrRecoveryReviewInvalid
	}
	if isRecoveryVerification(r.Action) != (r.SourceDispositionID != "") || (r.SourceDispositionID != "" && !validDispositionID(r.SourceDispositionID)) {
		return ErrRecoveryReviewInvalid
	}
	return nil
}

// RecoveryReview 只保存只读准备摘要和不可公开的绑定值，不保存凭据或原始恢复证据。
// 它不是执行授权；调用者仍须取得持久化占用、验证动作条件并原子写入审计。
type RecoveryReview struct {
	summary           RecoveryReviewSummary
	operator          string
	recordDigest      [sha256.Size]byte
	serverDigest      [sha256.Size]byte
	observationDigest [sha256.Size]byte
	service           *Service
}

func (r RecoveryReview) Summary() RecoveryReviewSummary { return r.summary }

func validReviewOperator(operator string) bool {
	return strings.TrimSpace(operator) == operator && len(operator) > 0 && len(operator) <= 160 && !strings.ContainsFunc(operator, unicode.IsControl)
}

func validRecoveryAction(action RecoveryAction) bool {
	return action == RecoveryActionRecord || action == RecoveryActionCommit || action == RecoveryActionRollback || isRecoveryVerification(action)
}

// PrepareRecoveryReview 在同一操作上限内读取前后记录，拒绝采样期间发生的配置或事务变化。
// 当前采样摘要只绑定本次准备，不补写到历史 Evidence，也不改变事务终态。
func (s *Service) PrepareRecoveryReview(ctx context.Context, id, operator string, action RecoveryAction, resolve func(context.Context, string) (Server, error)) (RecoveryReview, error) {
	// 重新核查必须通过独立入口绑定原处置，不能作为普通动作绕过来源检查。
	if isRecoveryVerification(action) {
		return RecoveryReview{}, ErrRecoveryReviewInvalid
	}
	return s.prepareRecoveryReview(ctx, id, operator, action, resolve)
}

func (s *Service) prepareRecoveryReview(ctx context.Context, id, operator string, action RecoveryAction, resolve func(context.Context, string) (Server, error)) (RecoveryReview, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validReviewOperator(operator) || !validRecoveryAction(action) || !validInspectionID(id) {
		return RecoveryReview{}, ErrRecoveryReviewInvalid
	}
	if s == nil || s.now == nil || s.journal == nil || s.factory == nil || resolve == nil {
		return RecoveryReview{}, ErrRecoveryInspectionUnavailable
	}
	started := s.now()
	record, recordDigest, serverDigest, err := s.reviewRecord(ctx, id, resolve)
	if err != nil {
		return RecoveryReview{}, err
	}
	if action != RecoveryActionRecord && record.Evidence == nil {
		return RecoveryReview{}, ErrRecoveryReviewEvidence
	}
	inspection, err := s.inspectPending(ctx, id, resolve, func(sampleRecord TransactionRecord, sampleServer Server) error {
		sampleRecordDigest, err := reviewDigest(sampleRecord)
		if err != nil {
			return err
		}
		sampleServerDigest, err := reviewServerDigest(sampleServer)
		if err != nil {
			return err
		}
		if recordDigest != sampleRecordDigest || serverDigest != sampleServerDigest {
			return ErrRecoveryReviewChanged
		}
		return nil
	})
	if err != nil {
		return RecoveryReview{}, err
	}
	_, currentRecord, currentServer, err := s.reviewRecord(ctx, id, resolve)
	if err != nil {
		return RecoveryReview{}, err
	}
	if recordDigest != currentRecord || serverDigest != currentServer {
		return RecoveryReview{}, ErrRecoveryReviewChanged
	}
	observedAt := inspection.ObservedAt
	inspection.ObservedAt = time.Time{}
	observationDigest, err := reviewDigest(inspection)
	if err != nil {
		return RecoveryReview{}, err
	}
	if err := ctx.Err(); err != nil {
		return RecoveryReview{}, err
	}
	expiresAt := started.Add(RecoveryReviewLifetime)
	if !s.now().Before(expiresAt) {
		return RecoveryReview{}, ErrRecoveryReviewExpired
	}
	return RecoveryReview{
		summary: RecoveryReviewSummary{TransactionID: id, ServerID: record.ServerID, Phase: record.Phase,
			Action: action, HistoricalEvidence: record.Evidence != nil, ObservedAt: observedAt, ExpiresAt: expiresAt},
		operator: operator, recordDigest: recordDigest, serverDigest: serverDigest, observationDigest: observationDigest, service: s,
	}, nil
}

// RecheckRecoveryReview 仅验证重新读取的状态与原准备一致，不消费确认凭据或执行远端变更。
// 后续执行器必须在持久化互斥范围内调用，并再次执行动作专属的锁、文件和严格 HTTPS 检查。
func (s *Service) RecheckRecoveryReview(ctx context.Context, review RecoveryReview, operator string, resolve func(context.Context, string) (Server, error)) error {
	if s == nil || review.service != s || review.operator != operator || !validReviewOperator(operator) {
		return ErrRecoveryReviewInvalid
	}
	if s.now == nil || !s.now().Before(review.summary.ExpiresAt) {
		return ErrRecoveryReviewExpired
	}
	var current RecoveryReview
	var err error
	if isRecoveryVerification(review.summary.Action) {
		current, err = s.prepareRecoveryReconciliation(ctx, review.summary.SourceDispositionID, operator, resolve)
	} else {
		current, err = s.PrepareRecoveryReview(ctx, review.summary.TransactionID, operator, review.summary.Action, resolve)
	}
	if err != nil {
		return err
	}
	if !s.now().Before(review.summary.ExpiresAt) {
		return ErrRecoveryReviewExpired
	}
	if current.recordDigest != review.recordDigest || current.serverDigest != review.serverDigest || current.observationDigest != review.observationDigest || current.summary.SourceDispositionID != review.summary.SourceDispositionID || current.summary.Action != review.summary.Action {
		return ErrRecoveryReviewChanged
	}
	return ctx.Err()
}

func reviewDigest(value any) ([sha256.Size]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, ErrRecoveryReviewInvalid
	}
	return sha256.Sum256(data), nil
}

func (s *Service) reviewRecord(ctx context.Context, id string, resolve func(context.Context, string) (Server, error)) (TransactionRecord, [sha256.Size]byte, [sha256.Size]byte, error) {
	fail := func(err error) (TransactionRecord, [sha256.Size]byte, [sha256.Size]byte, error) {
		return TransactionRecord{}, [sha256.Size]byte{}, [sha256.Size]byte{}, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	records, err := s.journal.ListPendingDeploymentTransactions(ctx)
	if err != nil {
		return fail(inspectionError(ctx, ErrRecoveryInspectionUnavailable))
	}
	var found *TransactionRecord
	for i := range records {
		if records[i].ID == id {
			if found != nil {
				return fail(ErrRecoveryInspectionRecord)
			}
			found = &records[i]
		}
	}
	if found == nil || validateInspectionRecord(*found) != nil || !validRecoverySummary(found.ServerID) ||
		(found.Phase != "" && !validRecoverySummary(found.Phase)) {
		return fail(ErrRecoveryInspectionRecord)
	}
	server, err := resolve(ctx, found.ServerID)
	if err != nil {
		return fail(inspectionError(ctx, ErrRecoveryInspectionUnavailable))
	}
	server = server.Normalize()
	if server.ID != found.ServerID || server.Validate() != nil || (found.Evidence != nil && validateRecoveryEvidence(*found, server) != nil) {
		return fail(ErrRecoveryInspectionIdentity)
	}
	recordDigest, err := reviewDigest(found)
	if err != nil {
		return fail(err)
	}
	serverDigest, err := reviewServerDigest(server)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return *found, recordDigest, serverDigest, nil
}

func reviewServerDigest(server Server) ([sha256.Size]byte, error) {
	return reviewDigest(struct {
		Server              Server
		CredentialReference string
	}{Server: server, CredentialReference: server.PasswordRef})
}
