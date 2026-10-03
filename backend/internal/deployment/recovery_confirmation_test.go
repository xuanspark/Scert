package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func confirmationFixture(t *testing.T) (*Service, RecoveryConfirmation, string) {
	t.Helper()
	server, client, record := recoveryPreflightFixture()
	service := NewServiceWithJournal(fixedClientFactory{client}, &memoryTransactionJournal{pending: []TransactionRecord{record}})
	confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", RecoveryActionRecord, func(context.Context, string) (Server, error) { return server, nil })
	if err != nil {
		t.Fatal(err)
	}
	return service, confirmation, token
}

func TestRecoveryConfirmationSingleUseAndPrivateBindings(t *testing.T) {
	service, confirmation, token := confirmationFixture(t)
	if len(token) != 64 || len(confirmation.DispositionID()) != 64 || token == confirmation.DispositionID() {
		t.Fatal("invalid confirmation entropy")
	}
	encoded, err := json.Marshal(confirmation)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("private confirmation serialized")
	}
	copy := confirmation
	review, intent, err := service.consumeRecoveryConfirmation(t.Context(), confirmation, "unix:0", token)
	if err != nil || review.Summary() != confirmation.Summary() || intent.Validate() != nil || intent.ID != confirmation.DispositionID() {
		t.Fatalf("confirmation failed: %v", err)
	}
	encoded, _ = json.Marshal(intent)
	if strings.Contains(string(encoded), token) {
		t.Fatal("raw token persisted")
	}
	if _, _, err := service.consumeRecoveryConfirmation(t.Context(), copy, "unix:0", token); !errors.Is(err, ErrRecoveryConfirmation) {
		t.Fatal("copied confirmation replayed")
	}
}

func TestRecoveryConfirmationRejectsAndConsumesInvalidAttempt(t *testing.T) {
	for _, scenario := range []string{"identity", "token", "oversized", "expired", "service", "cancelled", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			service, confirmation, token := confirmationFixture(t)
			originalService, originalToken := service, token
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			operator := "unix:0"
			switch scenario {
			case "identity":
				operator = "unix:1"
			case "token":
				token = strings.Repeat("0", 64)
			case "oversized":
				token = strings.Repeat("a", 8192)
			case "expired":
				service.now = func() time.Time { return confirmation.Summary().ExpiresAt }
			case "service":
				service = NewService(nil)
			case "cancelled":
				cancel()
			case "empty":
				confirmation = RecoveryConfirmation{}
			}
			if _, _, err := service.consumeRecoveryConfirmation(ctx, confirmation, operator, token); err == nil {
				t.Fatal("invalid confirmation accepted")
			}
			if _, _, err := originalService.consumeRecoveryConfirmation(t.Context(), confirmation, "unix:0", originalToken); err == nil {
				t.Fatal("failed attempt could be replayed")
			}
		})
	}
}

func TestRecoveryConfirmationConcurrentCopiesConsumeOnlyOnce(t *testing.T) {
	service, confirmation, token := confirmationFixture(t)
	var accepted atomic.Int32
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			if _, _, err := service.consumeRecoveryConfirmation(t.Context(), confirmation, "unix:0", token); err == nil {
				accepted.Add(1)
			}
		})
	}
	group.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d confirmations", accepted.Load())
	}
}
