package deployment

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type inspectionCancelClient struct {
	*inspectionClient
	cancel context.CancelFunc
	stage  string
}

func (c *inspectionCancelClient) Exists(ctx context.Context, path string) (bool, error) {
	if c.stage == "exists" || (c.stage == "lock-exists" && strings.HasSuffix(path, ".scert-lock")) {
		c.cancel()
		return false, errors.New("sensitive-read-error")
	}
	return c.memoryClient.Exists(ctx, path)
}

func (c *inspectionCancelClient) FileDigest(ctx context.Context, path string) (string, error) {
	if c.stage == "digest" {
		c.cancel()
		return "", errors.New("sensitive-read-error")
	}
	return c.memoryClient.FileDigest(ctx, path)
}

func (c *inspectionCancelClient) ReadLockOwner(ctx context.Context, path string) (string, error) {
	if c.stage == "owner" {
		c.cancel()
		return "", errors.New("sensitive-read-error")
	}
	return c.memoryClient.ReadLockOwner(ctx, path)
}

func TestRecoveryInspectionCancellationBoundaries(t *testing.T) {
	for _, stage := range []string{"resolve", "exists", "digest", "lock-exists", "owner"} {
		t.Run(stage, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &inspectionCancelClient{inspectionClient: &inspectionClient{memoryClient: memory}, cancel: cancel, stage: stage}
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			factory := &inspectionFactory{client: client}
			service := NewServiceWithJournal(factory, journal)
			result, err := service.InspectPending(ctx, record.ID, func(ctx context.Context, id string) (Server, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("inspection lacks bounded deadline")
				}
				if stage == "resolve" {
					cancel()
					return Server{}, errors.New("sensitive-resolve-error")
				}
				return server, nil
			})
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, RecoveryInspection{}) {
				t.Fatalf("cancellation lost: %+v %v", result, err)
			}
			if client.closed != factory.calls || client.mutations != 0 || len(journal.updated) != 0 || len(journal.finished) != 0 {
				t.Fatal("cancellation changed state or leaked client")
			}
		})
	}
}

func TestRecoveryInspectionPreservesEarlierDeadline(t *testing.T) {
	server, memory, record := recoveryPreflightFixture()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	client := &inspectionClient{memoryClient: memory}
	service := NewServiceWithJournal(fixedClientFactory{client}, &memoryTransactionJournal{pending: []TransactionRecord{record}})
	_, err := service.InspectPending(ctx, record.ID, func(ctx context.Context, id string) (Server, error) {
		actual, ok := ctx.Deadline()
		if !ok || !actual.Equal(deadline) {
			t.Fatal("caller deadline extended")
		}
		return server, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
