package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
)

type inspectionClient struct {
	*memoryClient
	closed      int
	mutations   int
	readError   bool
	afterDigest func()
}

func (c *inspectionClient) Close() error { c.closed++; return nil }
func (c *inspectionClient) Lock(context.Context, string) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) Unlock(context.Context, string) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) MkdirAll(context.Context, string) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) Upload(context.Context, string, []byte, uint32) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) Rename(context.Context, string, string) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) Remove(context.Context, string) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) Run(context.Context, []string) (CommandResult, error) {
	c.mutations++
	return CommandResult{}, errors.New("mutation")
}
func (c *inspectionClient) CreateOwnedLock(context.Context, string, string) error {
	c.mutations++
	return errors.New("mutation")
}
func (c *inspectionClient) FileDigest(ctx context.Context, path string) (string, error) {
	if c.readError {
		return "", errors.New("sensitive-read-error")
	}
	digest, err := c.memoryClient.FileDigest(ctx, path)
	if c.afterDigest != nil {
		c.afterDigest()
	}
	return digest, err
}

type inspectionFactory struct {
	client Client
	calls  int
}

func (f *inspectionFactory) Connect(context.Context, Server) (Client, error) {
	f.calls++
	return f.client, nil
}

func TestRecoveryInspectionReadOnly(t *testing.T) {
	for _, scenario := range []string{"trusted", "legacy", "missing", "mismatch", "foreign-lock", "ownerless-lock", "read-failure", "cancelled-read"} {
		t.Run(scenario, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			if scenario == "legacy" {
				record.Evidence = nil
			}
			if scenario == "missing" {
				clear(memory.files)
			}
			if scenario == "mismatch" {
				memory.files[record.Paths[0]] = memoryFile{data: []byte("untrusted-content")}
			}
			if scenario == "foreign-lock" {
				memory.files[record.LockPaths[0]+"/owner"] = memoryFile{data: []byte(contentDigest([]byte("foreign")))}
			}
			if scenario == "ownerless-lock" {
				delete(memory.files, record.LockPaths[0]+"/owner")
			}
			before := maps.Clone(memory.files)
			encodedBefore, _ := json.Marshal(record)
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			client := &inspectionClient{memoryClient: memory, readError: scenario == "read-failure"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled-read" {
				client.afterDigest = cancel
			}
			service := NewServiceWithJournal(fixedClientFactory{client}, journal)
			result, err := service.InspectPending(ctx, record.ID, func(context.Context, string) (Server, error) { return server, nil })
			if scenario == "read-failure" || scenario == "cancelled-read" {
				if err == nil || !reflect.DeepEqual(result, RecoveryInspection{}) {
					t.Fatal("incomplete inspection returned observations")
				}
				if strings.Contains(err.Error(), "sensitive-read-error") {
					t.Fatal("raw client error leaked")
				}
				if scenario == "cancelled-read" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Files) != 9 || len(result.Locks) != 3 || result.ObservedAt.IsZero() {
					t.Fatalf("incomplete result: %+v", result)
				}
				switch scenario {
				case "trusted":
					for _, file := range result.Files {
						if file.Comparison != "match" {
							t.Fatalf("unexpected file: %+v", file)
						}
					}
					for _, lock := range result.Locks {
						if lock.Ownership != "match" {
							t.Fatal(lock)
						}
					}
				case "legacy":
					if result.HistoricalEvidence {
						t.Fatal("legacy observations became historical evidence")
					}
					for _, file := range result.Files {
						if file.Comparison != "unrecorded" {
							t.Fatal(file)
						}
					}
					for _, lock := range result.Locks {
						if lock.Ownership != "unproven" {
							t.Fatal(lock)
						}
					}
				case "missing":
					for _, file := range result.Files {
						if file.Exists || file.Comparison != "missing" || file.Digest != "" {
							t.Fatal(file)
						}
					}
					for _, lock := range result.Locks {
						if lock.Exists || lock.Ownership != "missing" {
							t.Fatal(lock)
						}
					}
				case "mismatch":
					if result.Files[0].Comparison != "mismatch" {
						t.Fatal(result.Files[0])
					}
				case "foreign-lock":
					if result.Locks[0].Ownership != "foreign" {
						t.Fatal(result.Locks[0])
					}
				case "ownerless-lock":
					if result.Locks[0].Ownership != "unproven" {
						t.Fatal(result.Locks[0])
					}
				}
				data, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{server.PasswordRef, contentDigest([]byte("preflight-owner")), "untrusted-content", "sensitive-read-error"} {
					if strings.Contains(string(data), secret) {
						t.Fatalf("secret leaked: %s", secret)
					}
				}
			}
			encodedAfter, _ := json.Marshal(journal.pending[0])
			if !reflect.DeepEqual(before, memory.files) || string(encodedBefore) != string(encodedAfter) || client.mutations != 0 || len(journal.finished) != 0 || len(journal.updated) != 0 {
				t.Fatal("inspection modified remote files or transaction state")
			}
			if client.closed != 1 {
				t.Fatalf("client close count: %d", client.closed)
			}
		})
	}
}

func TestRecoveryInspectionRejectsBeforeConnection(t *testing.T) {
	for _, scenario := range []string{"unknown-id", "unsafe-id", "duplicate-id", "cancelled", "resolver-error", "wrong-server", "changed-host", "changed-command", "bad-path", "control-path", "duplicate-path", "nested-path", "overlap-lock", "bad-digest", "terminal-record"} {
		t.Run(scenario, func(t *testing.T) {
			server, memory, record := recoveryPreflightFixture()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			id := record.ID
			switch scenario {
			case "unknown-id":
				id = "unknown"
			case "unsafe-id":
				id = "../escape"
			case "cancelled":
				cancel()
			case "wrong-server":
				server.ID = "other"
			case "changed-host":
				server.HostKeyFingerprint = "SHA256:changed"
			case "changed-command":
				server.ReloadCommand = []string{"sh", "-c", "unexpected"}
			case "bad-path":
				record.Paths[0] = "/etc/scert/../shadow"
			case "control-path":
				record.Paths[0] = "/etc/scert/\x1bfile"
			case "duplicate-path":
				record.Paths[0] = record.Paths[1]
			case "nested-path":
				record.Paths[0] = record.Paths[1] + "/child"
			case "overlap-lock":
				record.Paths[0] = record.Paths[1] + ".scert-lock"
			case "bad-digest":
				record.Evidence.Files[record.Paths[0]] = "bad"
			case "terminal-record":
				record.Status = TransactionStatusCommitted
			}
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			if scenario == "duplicate-id" {
				journal.pending = append(journal.pending, record)
			}
			factory := &inspectionFactory{client: memory}
			service := NewServiceWithJournal(factory, journal)
			result, err := service.InspectPending(ctx, id, func(context.Context, string) (Server, error) {
				if scenario == "resolver-error" {
					return Server{}, errors.New("secret-resolver-error")
				}
				return server, nil
			})
			if err == nil || !reflect.DeepEqual(result, RecoveryInspection{}) || factory.calls != 0 {
				t.Fatalf("unsafe connection or result: %+v, %v, %d", result, err, factory.calls)
			}
			if strings.Contains(err.Error(), "secret-resolver-error") {
				t.Fatal("resolver error leaked")
			}
			if len(journal.finished) != 0 || len(journal.updated) != 0 {
				t.Fatal("rejected request modified journal")
			}
		})
	}
}
