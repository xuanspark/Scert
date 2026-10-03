package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRecoveryReviewDispositionIntent(t *testing.T) {
	server, memory, record := recoveryPreflightFixture()
	journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
	service := NewServiceWithJournal(fixedClientFactory{&inspectionClient{memoryClient: memory}}, journal)
	resolve := func(context.Context, string) (Server, error) { return server, nil }
	review, err := service.PrepareRecoveryReview(t.Context(), record.ID, "unix:0", RecoveryActionCommit, resolve)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := review.DispositionIntent(strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil || intent.Validate() != nil || !intent.MatchesRecord(record) {
		t.Fatal("review bindings not retained", err)
	}
	if !intent.AllowsResult(RecoveryResultCommitted) || intent.AllowsResult(RecoveryResultRolledBack) || intent.AllowsResult(RecoveryResultObserved) {
		t.Fatal("action result isolation failed")
	}
	encoded, _ := json.Marshal(RecoveryDisposition{Intent: intent})
	if strings.Contains(string(encoded), intent.RequestKey) || strings.Contains(string(encoded), intent.RecordDigest) || strings.Contains(string(encoded), "unix:0") {
		t.Fatal("internal bindings exposed in public JSON")
	}
	for _, value := range []string{"", "token-secret", strings.Repeat("A", 64), strings.Repeat("0", 64), strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		if _, err := review.DispositionIntent(value, strings.Repeat("b", 64)); !errors.Is(err, ErrRecoveryDispositionInvalid) {
			t.Fatal("invalid identifier accepted")
		}
		if _, err := review.DispositionIntent(strings.Repeat("a", 64), value); !errors.Is(err, ErrRecoveryDispositionInvalid) {
			t.Fatal("invalid request key accepted")
		}
	}
	record.Message = "changed"
	if intent.MatchesRecord(record) {
		t.Fatal("changed transaction accepted")
	}
	service.now = func() time.Time { return review.Summary().ExpiresAt }
	if _, err := review.DispositionIntent(strings.Repeat("a", 64), strings.Repeat("b", 64)); !errors.Is(err, ErrRecoveryReviewExpired) {
		t.Fatal("expired preparation accepted")
	}
	if _, err := (RecoveryReview{}).DispositionIntent(strings.Repeat("a", 64), strings.Repeat("b", 64)); err == nil {
		t.Fatal("zero review accepted")
	}
}
