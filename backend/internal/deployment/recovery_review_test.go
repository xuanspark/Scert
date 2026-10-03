package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRecoveryReviewSummaryBoundary(t *testing.T) {
	now := time.Now().UTC()
	valid := RecoveryReviewSummary{TransactionID: "tx-1", ServerID: "历史服务器", Action: RecoveryActionRecord, ObservedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid legacy summary rejected: %v", err)
	}
	for _, mode := range []string{"id", "server", "phase", "action", "evidence", "observed", "expired", "lifetime"} {
		value := valid
		switch mode {
		case "id":
			value.TransactionID = "../bad"
		case "server":
			value.ServerID = strings.Repeat("s", 161)
		case "phase":
			value.Phase = "phase\n"
		case "action":
			value.Action = "unknown"
		case "evidence":
			value.Action = RecoveryActionCommit
		case "observed":
			value.ObservedAt = time.Time{}
		case "expired":
			value.ExpiresAt = now
		case "lifetime":
			value.ExpiresAt = now.Add(time.Hour)
		}
		if value.Validate() == nil {
			t.Fatalf("invalid summary accepted: %s", mode)
		}
	}
}

func TestRecoveryReviewReadOnlyBindings(t *testing.T) {
	for _, action := range []RecoveryAction{RecoveryActionRecord, RecoveryActionCommit, RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			before := maps.Clone(memory.files)
			encodedBefore, _ := json.Marshal(record)
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			client := &inspectionClient{memoryClient: memory}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			review, err := service.PrepareRecoveryReview(t.Context(), record.ID, "unix:0", action, resolve)
			if err != nil {
				t.Fatal(err)
			}
			summary := review.Summary()
			if summary.TransactionID != record.ID || summary.ServerID != record.ServerID || summary.Action != action ||
				!summary.HistoricalEvidence || summary.ObservedAt.IsZero() || !summary.ExpiresAt.After(summary.ObservedAt) {
				t.Fatal("invalid preparation summary")
			}
			summary.TransactionID = "tampered"
			if err := service.RecheckRecoveryReview(t.Context(), review, "unix:0", resolve); err != nil {
				t.Fatal(err)
			}
			encodedAfter, _ := json.Marshal(journal.pending[0])
			if client.mutations != 0 || !reflect.DeepEqual(before, memory.files) || string(encodedAfter) != string(encodedBefore) {
				t.Fatal("review changed original transaction or remote state")
			}
			encoded, _ := json.Marshal(review)
			if string(encoded) != "{}" {
				t.Fatal("private bindings serialized")
			}
			encoded, _ = json.Marshal(review.Summary())
			if strings.Contains(string(encoded), record.Evidence.LockToken) || strings.Contains(string(encoded), "credential") {
				t.Fatal("public summary contains secret evidence")
			}
		})
	}
}

func TestRecoveryReviewLegacyCannotPrepareWrites(t *testing.T) {
	for _, action := range []RecoveryAction{RecoveryActionRecord, RecoveryActionCommit, RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			record.Evidence = nil
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			client := &inspectionClient{memoryClient: memory}
			factory := &inspectionFactory{client: client}
			service := NewServiceWithJournal(factory, journal)
			review, err := service.PrepareRecoveryReview(t.Context(), record.ID, "unix:0", action, func(context.Context, string) (Server, error) { return server, nil })
			if action == RecoveryActionRecord {
				if err != nil || review.Summary().HistoricalEvidence {
					t.Fatalf("legacy read failed: %v", err)
				}
			} else if !errors.Is(err, ErrRecoveryReviewEvidence) || factory.calls != 0 || review.Summary().TransactionID != "" {
				t.Fatal("legacy write preparation accepted or connected remotely")
			}
			if journal.pending[0].Evidence != nil || client.mutations != 0 {
				t.Fatal("legacy evidence manufactured")
			}
		})
	}
}

func TestRecoveryReviewRejectsChanges(t *testing.T) {
	for _, scenario := range []string{"phase", "message", "time", "target", "backup", "temporary", "lock", "server-user", "server-password-reference", "server-fingerprint", "server-command", "server-path", "record-missing"} {
		t.Run(scenario, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			client := &inspectionClient{memoryClient: memory}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			review, err := service.PrepareRecoveryReview(t.Context(), record.ID, "unix:0", RecoveryActionRollback, resolve)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "phase":
				journal.pending[0].Phase = TransactionPhaseCommitting
			case "message":
				journal.pending[0].Message = "changed"
			case "time":
				journal.pending[0].UpdatedAt = time.Now().UTC()
			case "target":
				memory.files[record.Paths[0]] = memoryFile{data: []byte("changed")}
			case "backup":
				memory.files[record.Backups[0].Saved] = memoryFile{data: []byte("changed")}
			case "temporary":
				delete(memory.files, record.TemporaryPaths[0])
			case "lock":
				memory.files[record.LockPaths[0]+"/owner"] = memoryFile{data: []byte(contentDigest([]byte("foreign")))}
			case "server-user":
				server.Username = "other"
			case "server-password-reference":
				server.PasswordRef = "changed-reference"
			case "server-fingerprint":
				server.HostKeyFingerprint = "SHA256:" + strings.Repeat("B", 43)
			case "server-command":
				server.ReloadCommand = []string{"systemctl", "reload", "other"}
			case "server-path":
				server.CertificatePath = "/different/cert.pem"
			case "record-missing":
				journal.pending = nil
			}
			if err := service.RecheckRecoveryReview(t.Context(), review, "unix:0", resolve); err == nil {
				t.Fatal("changed state reused old preparation")
			}
			if client.mutations != 0 {
				t.Fatal("recheck modified remote state")
			}
		})
	}
}

func TestRecoveryReviewRejectsChangesDuringSampling(t *testing.T) {
	for _, scenario := range []string{"record", "server", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			client := &inspectionClient{memoryClient: memory}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client.afterDigest = func() {
				switch scenario {
				case "record":
					journal.pending[0].Message = "changed"
				case "server":
					server.PasswordRef = "changed-reference"
				case "cancel":
					cancel()
				}
			}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			review, err := service.PrepareRecoveryReview(ctx, record.ID, "unix:0", RecoveryActionRecord, func(context.Context, string) (Server, error) { return server, nil })
			want := ErrRecoveryReviewChanged
			if scenario == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || review.Summary().TransactionID != "" || client.mutations != 0 {
				t.Fatalf("sampling change was not rejected: %v", err)
			}
		})
	}
}

func TestRecoveryReviewExpiryAndIdentity(t *testing.T) {
	for _, scenario := range []string{"expired", "expires-during-recheck", "operator", "other-service", "zero", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			client := &inspectionClient{memoryClient: memory}
			factory := &inspectionFactory{client: client}
			service := NewServiceWithJournal(factory, journal)
			now := time.Now()
			service.now = func() time.Time { return now }
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			review, err := service.PrepareRecoveryReview(t.Context(), record.ID, "unix:0", RecoveryActionRecord, resolve)
			if err != nil {
				t.Fatal(err)
			}
			calls := factory.calls
			operator := "unix:0"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := ErrRecoveryReviewInvalid
			switch scenario {
			case "expired":
				now = review.Summary().ExpiresAt
				want = ErrRecoveryReviewExpired
			case "expires-during-recheck":
				client.afterDigest = func() { now = review.Summary().ExpiresAt }
				want = ErrRecoveryReviewExpired
			case "operator":
				operator = "unix:1"
			case "other-service":
				service = NewServiceWithJournal(factory, journal)
			case "zero":
				review = RecoveryReview{}
			case "canceled":
				cancel()
				want = context.Canceled
			}
			if err := service.RecheckRecoveryReview(ctx, review, operator, resolve); !errors.Is(err, want) {
				t.Fatalf("wrong expiry or identity result: %v", err)
			}
			if scenario != "expires-during-recheck" && factory.calls != calls {
				t.Fatal("rejected identity or expiry read remote state")
			}
			if client.mutations != 0 {
				t.Fatal("rejected review mutated remote state")
			}
		})
	}
}

func TestRecoveryReviewBindsActualSampleConfiguration(t *testing.T) {
	server, memory, record := recoveryPreflightFixture()
	journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
	client := &inspectionClient{memoryClient: memory}
	factory := &inspectionFactory{client: client}
	service := NewServiceWithJournal(factory, journal)
	reads := 0
	resolve := func(context.Context, string) (Server, error) {
		reads++
		value := server
		if reads == 2 {
			value.PasswordRef = "transient-configuration"
		}
		return value, nil
	}
	review, err := service.PrepareRecoveryReview(t.Context(), record.ID, "unix:0", RecoveryActionRecord, resolve)
	if !errors.Is(err, ErrRecoveryReviewChanged) || review.Summary().TransactionID != "" || factory.calls != 0 {
		t.Fatal("sample used a different configuration from its binding")
	}
}

func TestRecoveryReviewRejectsInvalidPreparation(t *testing.T) {
	for _, scenario := range []string{"operator-empty", "operator-space", "operator-control", "operator-large", "action", "id", "nil-resolver", "nil-service", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			factory := &inspectionFactory{client: &inspectionClient{memoryClient: memory}}
			service := NewServiceWithJournal(factory, journal)
			resolve := func(context.Context, string) (Server, error) { return server, nil }
			operator, id, action := "unix:0", record.ID, RecoveryActionRecord
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "operator-empty":
				operator = ""
			case "operator-space":
				operator = " unix:0"
			case "operator-control":
				operator = "unix:0\n"
			case "operator-large":
				operator = strings.Repeat("x", 161)
			case "action":
				action = "unknown"
			case "id":
				id = "../bad"
			case "nil-resolver":
				resolve = nil
			case "nil-service":
				service = nil
			case "canceled":
				cancel()
			}
			review, err := service.PrepareRecoveryReview(ctx, id, operator, action, resolve)
			if err == nil || review.Summary().TransactionID != "" || factory.calls != 0 {
				t.Fatal("invalid preparation reached remote state or returned a review")
			}
		})
	}
}
