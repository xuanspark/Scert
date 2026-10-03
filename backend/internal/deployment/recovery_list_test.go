package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRecoveryListPaginationAndRedaction(t *testing.T) {
	journal := &memoryTransactionJournal{}
	for i := 12; i > 0; i-- {
		journal.pending = append(journal.pending, TransactionRecord{
			ID: fmt.Sprintf("tx-%02d", i), ServerID: "server-1", Phase: TransactionPhaseInstalled,
			Status: TransactionStatusRecoveryRequired, Message: "secret-message", ServerName: "secret-name",
			Paths: []string{"secret-path"}, Evidence: &RecoveryEvidence{},
		})
	}
	factory := &inspectionFactory{}
	service := NewServiceWithJournal(factory, journal)
	cursor := ""
	var ids []string
	for _, size := range []int{5, 5, 2} {
		page, err := service.ListPendingRecoveries(t.Context(), cursor)
		if err != nil || len(page.Items) != size {
			t.Fatalf("page: %+v, %v", page, err)
		}
		data, _ := json.Marshal(page)
		if strings.Contains(string(data), "secret-") || len(data) > 16<<10 {
			t.Fatal("summary leaked data or exceeded frame")
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
			if !item.HistoricalEvidence {
				t.Fatal("evidence presence lost")
			}
		}
		cursor = page.NextCursor
	}
	if cursor != "" || len(ids) != 12 || ids[0] != "tx-01" || ids[11] != "tx-12" ||
		factory.calls != 0 || len(journal.updated) != 0 || len(journal.finished) != 0 {
		t.Fatalf("list mutated state, connected, or lost ordering: %v", ids)
	}
}

func TestRecoveryListRejectsInvalidRecords(t *testing.T) {
	for _, scenario := range []string{"duplicate", "id", "server", "phase", "status", "cursor", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			record := TransactionRecord{ID: "tx-1", ServerID: "server-1", Phase: TransactionPhaseInstalled, Status: TransactionStatusPending}
			journal := &memoryTransactionJournal{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cursor := ""
			switch scenario {
			case "duplicate":
				journal.pending = append(journal.pending, record)
			case "id":
				record.ID = "../bad"
			case "server":
				record.ServerID = strings.Repeat("x", 161)
			case "phase":
				record.Phase = "\x1b[2J"
			case "status":
				record.Status = TransactionStatusCommitted
			case "cursor":
				cursor = "../bad"
			case "cancelled":
				cancel()
			}
			journal.pending = append(journal.pending, record)
			page, err := NewServiceWithJournal(nil, journal).ListPendingRecoveries(ctx, cursor)
			if err == nil || !reflect.DeepEqual(page, PendingRecoveryPage{}) {
				t.Fatal("invalid list returned data")
			}
			if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
