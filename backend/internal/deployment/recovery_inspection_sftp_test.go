//go:build linux

package deployment

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestRecoveryInspectionOverPasswordSFTP(t *testing.T) {
	signer := newTestECDSAHostSigner(t)
	endpoint := startTestPasswordSSHServerWithHandler(t, func(password string) error {
		if password != "test-password" {
			return errors.New("invalid test password")
		}
		return nil
	}, serveTestSFTP, signer)
	for _, scenario := range []string{"trusted", "legacy", "target-symlink", "target-directory", "target-unreadable", "target-oversized", "owner-oversized", "owner-symlink", "lock-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			server, memory, record := recoveryPreflightFixture(root)
			model := testSSHServerModel(endpoint, "secret://ssh/inspection", ssh.FingerprintSHA256(signer.PublicKey()))
			server.Address, server.Port, server.Username = model.Address, model.Port, model.Username
			server.AuthMethod, server.PasswordRef, server.HostKeyFingerprint = model.AuthMethod, model.PasswordRef, model.HostKeyFingerprint
			record.Evidence.Address, record.Evidence.Port, record.Evidence.HostKeyFingerprint = server.Address, server.Port, server.HostKeyFingerprint
			for path, file := range memory.files {
				if strings.HasSuffix(path, ".scert-lock") {
					if err := os.MkdirAll(path, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, file.data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			target := record.Paths[0]
			lock := record.LockPaths[0]
			owner := lock + "/owner"
			switch scenario {
			case "legacy":
				record.Evidence = nil
			case "target-symlink", "owner-symlink", "lock-symlink":
				name := target
				if scenario == "owner-symlink" {
					name = owner
				}
				if scenario == "lock-symlink" {
					name = lock
				}
				if err := os.Rename(name, name+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(name+".original", name); err != nil {
					t.Fatal(err)
				}
			case "target-directory":
				if err := os.Rename(target, target+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "target-unreadable":
				if os.Geteuid() == 0 {
					t.Skip("requires unprivileged Linux test user")
				}
				if err := os.Chmod(target, 0); err != nil {
					t.Fatal(err)
				}
			case "target-oversized":
				if err := os.Truncate(target, 16*1024*1024+1); err != nil {
					t.Fatal(err)
				}
			case "owner-oversized":
				if err := os.Truncate(owner, 129); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotInspectionFiles(t, root)
			journal := &memoryTransactionJournal{pending: []TransactionRecord{record}}
			service := NewServiceWithJournal(SSHFactory{
				Secrets: staticSecretReader{server.PasswordRef: {"password": "test-password"}},
				Timeout: 5 * time.Second,
			}, journal)
			result, err := service.InspectPending(t.Context(), record.ID, func(context.Context, string) (Server, error) { return server, nil })
			if scenario == "trusted" || scenario == "legacy" {
				if err != nil || len(result.Files) != 9 || len(result.Locks) != 3 || result.HistoricalEvidence != (scenario == "trusted") {
					t.Fatalf("inspection failed: %+v %v", result, err)
				}
				for _, file := range result.Files {
					want := "match"
					if scenario == "legacy" {
						want = "unrecorded"
					}
					if file.Comparison != want {
						t.Fatalf("unexpected observation: %+v", file)
					}
				}
			} else if !errors.Is(err, ErrRecoveryInspectionRead) || !reflect.DeepEqual(result, RecoveryInspection{}) {
				t.Fatalf("unsafe or unreadable file was accepted: %+v %v", result, err)
			}
			if !reflect.DeepEqual(before, snapshotInspectionFiles(t, root)) || len(journal.updated) != 0 || len(journal.finished) != 0 {
				t.Fatal("read-only inspection changed files or journal")
			}
		})
	}
}

func snapshotInspectionFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += target
		} else if info.Mode().IsRegular() && info.Mode().Perm() != 0 {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += contentDigest(data)
		}
		result[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
