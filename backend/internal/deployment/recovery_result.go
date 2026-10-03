package deployment

import (
	"context"
	"strings"
	"time"
)

// RecoveryDispositionSummary 是本机查询的明确输出边界，不包含请求键、绑定摘要或操作者凭据。
type RecoveryDispositionSummary struct {
	SourceDispositionID string         `json:"source_disposition_id,omitempty"`
	ID                  string         `json:"id"`
	TransactionID       string         `json:"transaction_id"`
	ServerID            string         `json:"server_id"`
	Action              RecoveryAction `json:"action"`
	Status              string         `json:"status"`
	ResultCode          string         `json:"result_code"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

func (r RecoveryDisposition) Summary() RecoveryDispositionSummary {
	return RecoveryDispositionSummary{
		SourceDispositionID: r.Intent.SourceDispositionID,
		ID:                  r.Intent.ID, TransactionID: r.Intent.TransactionID, ServerID: r.Intent.ServerID,
		Action: r.Intent.Action, Status: r.Status, ResultCode: r.ResultCode,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func validDispositionID(id string) bool {
	return validDigest(id) && id == strings.ToLower(id) && id != strings.Repeat("0", 64)
}

func (r RecoveryDispositionSummary) Validate() error {
	if isRecoveryVerification(r.Action) != (r.SourceDispositionID != "") || (r.SourceDispositionID != "" && (!validDispositionID(r.SourceDispositionID) || r.SourceDispositionID == r.ID)) {
		return ErrRecoveryDispositionInvalid
	}
	if !validDispositionID(r.ID) || !validInspectionID(r.TransactionID) || !validRecoverySummary(r.ServerID) ||
		!validRecoveryAction(r.Action) || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return ErrRecoveryDispositionInvalid
	}
	intent := RecoveryDispositionIntent{Action: r.Action}
	switch r.Status {
	case "running":
		if r.ResultCode == "" {
			return nil
		}
	case "succeeded":
		if r.ResultCode != RecoveryResultRejected && r.ResultCode != RecoveryResultUncertain && intent.AllowsResult(r.ResultCode) {
			return nil
		}
	case RecoveryResultRejected, RecoveryResultUncertain:
		if r.ResultCode == r.Status {
			return nil
		}
	}
	return ErrRecoveryDispositionInvalid
}

// GetRecoveryDisposition 不取得写操作门，执行中或重启后均可查询；查询本身不会重放动作或释放占用。
func (s *Service) GetRecoveryDisposition(ctx context.Context, id string) (RecoveryDispositionSummary, error) {
	if !validDispositionID(id) {
		return RecoveryDispositionSummary{}, ErrRecoveryDispositionInvalid
	}
	if s == nil {
		return RecoveryDispositionSummary{}, ErrRecoveryDispositionUnavailable
	}
	journal, ok := s.journal.(RecoveryDispositionJournal)
	if !ok {
		return RecoveryDispositionSummary{}, ErrRecoveryDispositionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	item, err := journal.GetRecoveryDisposition(ctx, id)
	if err != nil || ctx.Err() != nil || item.Intent.ID != id || item.Intent.Validate() != nil {
		return RecoveryDispositionSummary{}, ErrRecoveryDispositionUnavailable
	}
	summary := item.Summary()
	if summary.Validate() != nil {
		return RecoveryDispositionSummary{}, ErrRecoveryDispositionUnavailable
	}
	return summary, nil
}
