package deployment

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestRecoveryPreflightOverPasswordSFTP 使用回环 SSH 和真实临时文件核验协议行为，不连接业务服务器。
func TestRecoveryPreflightOverPasswordSFTP(t *testing.T) {
	hostSigner := newTestECDSAHostSigner(t)
	endpoint := startTestPasswordSSHServerWithHandler(t, func(password string) error {
		if password != "test-password" {
			return errors.New("隔离测试密码不匹配")
		}
		return nil
	}, serveTestSFTP, hostSigner)
	for _, scenario := range []string{"foreign_lock", "missing_owner", "changed_backup", "changed_target", "changed_temporary", "unrecorded_temporary", "valid_restore"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			server, memory, r := recoveryPreflightFixture(root)
			model := testSSHServerModel(endpoint, "secret://ssh/preflight", ssh.FingerprintSHA256(hostSigner.PublicKey()))
			server.Address, server.Port, server.Username = model.Address, model.Port, model.Username
			server.AuthMethod, server.PasswordRef, server.HostKeyFingerprint = model.AuthMethod, model.PasswordRef, model.HostKeyFingerprint
			r.Evidence.Address, r.Evidence.Port, r.Evidence.HostKeyFingerprint = server.Address, server.Port, server.HostKeyFingerprint
			r.ReloadAttempted, r.Phase = false, TransactionPhaseInstalled
			switch scenario {
			case "foreign_lock":
				memory.files[r.LockPaths[0]+"/owner"] = memoryFile{data: []byte(contentDigest([]byte("another-owner")))}
			case "missing_owner":
				delete(memory.files, r.LockPaths[0]+"/owner")
			case "changed_backup":
				memory.files[r.Backups[0].Saved] = memoryFile{data: []byte("unexpected-backup")}
			case "changed_target":
				memory.files[r.Paths[0]] = memoryFile{data: []byte("unexpected-target")}
			case "changed_temporary":
				memory.files[r.TemporaryPaths[2]] = memoryFile{data: []byte("unexpected-temporary")}
			case "unrecorded_temporary":
				delete(r.Evidence.TemporaryFiles, r.TemporaryPaths[2])
			}
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
			before := snapshotRecoveryDirectory(t, root)
			factory := SSHFactory{
				Secrets: staticSecretReader{server.PasswordRef: {"password": "test-password"}},
				Timeout: 5 * time.Second,
			}
			journal := &memoryTransactionJournal{pending: []TransactionRecord{r}}
			service := NewServiceWithJournal(factory, journal)
			err := service.RecoverPending(t.Context(), func(context.Context, string) (Server, error) { return server, nil })
			if scenario == "valid_restore" {
				if err != nil {
					t.Fatalf("真实 SFTP 可信恢复失败：%v", err)
				}
				if len(journal.finished) != 1 || journal.finished[0].status != TransactionStatusRolledBack {
					t.Fatal("真实 SFTP 恢复未完成")
				}
				for _, target := range r.Paths {
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "old" {
						t.Fatalf("真实文件恢复错误：%s %v", target, err)
					}
				}
				if len(snapshotRecoveryDirectory(t, root)) != 4 {
					t.Fatal("恢复后应仅保留目录和三个原始文件")
				}
				return
			}
			if err == nil {
				t.Fatal("真实 SFTP 必须拒绝不可信恢复")
			}
			if !reflect.DeepEqual(before, snapshotRecoveryDirectory(t, root)) {
				t.Fatal("真实 SFTP 拒绝恢复时修改了磁盘现场")
			}
			if len(journal.finished) != 0 || len(journal.updated) != 1 || journal.updated[0].Status != TransactionStatusRecoveryRequired {
				t.Fatal("真实 SFTP 未保留待核查事务")
			}
		})
	}
}

func snapshotRecoveryDirectory(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = info.Mode().String()
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = fmt.Sprintf("%s:%x", info.Mode(), sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
