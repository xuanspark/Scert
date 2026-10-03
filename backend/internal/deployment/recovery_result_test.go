package deployment

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecoveryDispositionSummaryValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, action := range []RecoveryAction{RecoveryActionRecord, RecoveryActionCommit, RecoveryActionRollback} {
		for _, result := range []string{"", RecoveryResultObserved, RecoveryResultCommitted, RecoveryResultRolledBack, RecoveryResultRejected, RecoveryResultUncertain} {
			status := "succeeded"
			if result == "" {
				status = "running"
			}
			if result == RecoveryResultRejected || result == RecoveryResultUncertain {
				status = result
			}
			summary := RecoveryDispositionSummary{ID: strings.Repeat("a", 64), TransactionID: "transaction", ServerID: "server", Action: action, Status: status, ResultCode: result, CreatedAt: now, UpdatedAt: now}
			want := result == "" || (RecoveryDispositionIntent{Action: action}).AllowsResult(result)
			if (summary.Validate() == nil) != want {
				t.Fatalf("wrong result validation: %+v", summary)
			}
		}
	}
}

func TestRecoveryDispositionQueryIsReadOnlyAndPrivate(t *testing.T) {
	service, journal, _, _, _ := executorFixture(t)
	now := time.Now().UTC()
	journal.item = RecoveryDisposition{
		Intent: RecoveryDispositionIntent{
			ID: strings.Repeat("a", 64), RequestKey: strings.Repeat("b", 64), RecordDigest: strings.Repeat("c", 64),
			ServerDigest: strings.Repeat("d", 64), ObservationDigest: strings.Repeat("e", 64),
			TransactionID: "transaction", ServerID: "server", Operator: "unix:private", Action: RecoveryActionCommit,
		}, Status: "running", CreatedAt: now, UpdatedAt: now,
	}
	journal.claimed = true
	release, err := service.acquireOperation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result, err := service.GetRecoveryDisposition(t.Context(), journal.item.Intent.ID)
	if err != nil || result.Status != "running" || !journal.claimed || journal.beginCalls != 0 || journal.finishCalls != 0 {
		t.Fatal("query changed state or blocked behind writer", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{journal.item.Intent.RequestKey, journal.item.Intent.RecordDigest, journal.item.Intent.ServerDigest, journal.item.Intent.ObservationDigest, journal.item.Intent.Operator} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private binding leaked in result")
		}
	}
	if _, err := service.GetRecoveryDisposition(t.Context(), strings.Repeat("f", 64)); err == nil {
		t.Fatal("wrong result identifier accepted")
	}
	journal.item.Status = "succeeded"
	journal.item.ResultCode = RecoveryResultRolledBack
	if _, err := service.GetRecoveryDisposition(t.Context(), journal.item.Intent.ID); err == nil {
		t.Fatal("inconsistent action result accepted")
	}
}
