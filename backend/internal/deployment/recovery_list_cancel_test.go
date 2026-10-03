package deployment

import (
	"context"
	"errors"
	"testing"
)

type cancelledRecoveryListJournal struct {
	memoryTransactionJournal
	cancel context.CancelFunc
}

func (j *cancelledRecoveryListJournal) ListPendingDeploymentTransactions(context.Context) ([]TransactionRecord, error) {
	j.cancel()
	return nil, nil
}

func TestRecoveryListCancellationAfterEmptyRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	journal := &cancelledRecoveryListJournal{cancel: cancel}
	page, err := NewServiceWithJournal(nil, journal).ListPendingRecoveries(ctx, "")
	if !errors.Is(err, context.Canceled) || page.Items != nil {
		t.Fatal("cancelled empty result accepted")
	}
}

func TestRecoveryListLegacyWithoutPhase(t *testing.T) {
	journal := &memoryTransactionJournal{pending: []TransactionRecord{{ID: "legacy", ServerID: "server-1", Status: TransactionStatusRecoveryRequired}}}
	page, err := NewServiceWithJournal(nil, journal).ListPendingRecoveries(t.Context(), "")
	if err != nil || len(page.Items) != 1 || page.Items[0].HistoricalEvidence {
		t.Fatal("legacy record omitted or evidence invented")
	}
}
