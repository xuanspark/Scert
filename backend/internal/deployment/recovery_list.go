package deployment

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
)

const RecoveryPageSize = 5

type PendingRecovery struct {
	ID                 string `json:"id"`
	ServerID           string `json:"server_id"`
	Phase              string `json:"phase"`
	Status             string `json:"status"`
	HistoricalEvidence bool   `json:"historical_evidence"`
}

type PendingRecoveryPage struct {
	Items      []PendingRecovery `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ListPendingRecoveries 只返回有界摘要，不返回现场路径、错误详情、凭据或恢复证据内容。
// 游标按事务标识排序；分页期间现场可能变化，不提供一致性快照承诺。
func (s *Service) ListPendingRecoveries(ctx context.Context, after string) (PendingRecoveryPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return PendingRecoveryPage{}, err
	}
	if s == nil || s.journal == nil {
		return PendingRecoveryPage{}, ErrRecoveryInspectionUnavailable
	}
	if after != "" && !validInspectionID(after) {
		return PendingRecoveryPage{}, ErrRecoveryInspectionRecord
	}
	records, err := s.journal.ListPendingDeploymentTransactions(ctx)
	if err != nil {
		return PendingRecoveryPage{}, inspectionError(ctx, ErrRecoveryInspectionUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return PendingRecoveryPage{}, err
	}
	items := make([]PendingRecovery, 0, len(records))
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return PendingRecoveryPage{}, err
		}
		if !validInspectionID(record.ID) || seen[record.ID] || !validRecoverySummary(record.ServerID) ||
			(record.Phase != "" && !validRecoverySummary(record.Phase)) ||
			(record.Status != TransactionStatusPending && record.Status != TransactionStatusRecoveryRequired) {
			return PendingRecoveryPage{}, ErrRecoveryInspectionRecord
		}
		seen[record.ID] = true
		if record.ID > after {
			items = append(items, PendingRecovery{ID: record.ID, ServerID: record.ServerID,
				Phase: record.Phase, Status: record.Status, HistoricalEvidence: record.Evidence != nil})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if err := ctx.Err(); err != nil {
		return PendingRecoveryPage{}, err
	}
	page := PendingRecoveryPage{Items: items}
	if len(items) > RecoveryPageSize {
		page.Items = items[:RecoveryPageSize]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func validRecoverySummary(value string) bool {
	return len(value) > 0 && len(value) <= 160 && !strings.ContainsFunc(value, unicode.IsControl)
}
