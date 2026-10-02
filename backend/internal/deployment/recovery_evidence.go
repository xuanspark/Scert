package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
)

// RecoveryBaseline 只保存站点身份，不保存证书内容、私钥或登录凭据。
type RecoveryBaseline struct {
	Endpoint            string   `json:"endpoint"`
	Domains             []string `json:"domains"`
	PreviousFingerprint string   `json:"previous_fingerprint"`
	ExpectedFingerprint string   `json:"expected_fingerprint"`
}

// RecoveryEvidence 保存恢复所需的不可变身份与已核验决策。
type RecoveryEvidence struct {
	Version            int               `json:"version"`
	LockToken          string            `json:"lock_token"`
	Address            string            `json:"address"`
	Port               int               `json:"port"`
	HostKeyFingerprint string            `json:"host_key_fingerprint"`
	ValidationCommand  []string          `json:"validation_command"`
	ReloadCommand      []string          `json:"reload_command"`
	Files              map[string]string `json:"files"`
	TemporaryFiles     map[string]string `json:"temporary_files,omitempty"`
	Baseline           RecoveryBaseline  `json:"baseline"`
}

// EvidenceClient 通过原子符号链接保存锁归属，文件摘要不会将私钥传回业务层。
type EvidenceClient interface {
	CreateOwnedLock(context.Context, string, string) error
	ReadLockOwner(context.Context, string) (string, error)
	FileDigest(context.Context, string) (string, error)
}

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func checkOwnedLock(ctx context.Context, client Client, path, token string, allowMissing bool) error {
	reader, ok := client.(EvidenceClient)
	if !ok || !validDigest(token) {
		return errors.New("缺少可验证的部署锁归属信息")
	}
	owner, err := reader.ReadLockOwner(ctx, path)
	if err != nil {
		return err
	}
	if owner == "" && allowMissing {
		return nil
	}
	if owner != token {
		return errors.New("部署锁不属于本次事务，已保留现场")
	}
	return nil
}

func checkFileDigest(ctx context.Context, client Client, path, expected string) error {
	reader, ok := client.(EvidenceClient)
	if !ok || !validDigest(expected) {
		return errors.New("缺少可信文件摘要")
	}
	actual, err := reader.FileDigest(ctx, path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("文件身份与事务记录不一致：%s", path)
	}
	return nil
}

func validateRecoveryEvidence(record TransactionRecord, server Server) error {
	e := record.Evidence
	if e == nil || e.Version != 1 || !validDigest(e.LockToken) {
		return errors.New("历史事务缺少可信恢复证据，需核查现场，不自动接管")
	}
	if e.Address != server.Address || e.Port != server.Port || e.HostKeyFingerprint != server.HostKeyFingerprint ||
		!reflect.DeepEqual(e.ValidationCommand, server.ValidationCommand) || !reflect.DeepEqual(e.ReloadCommand, server.ReloadCommand) {
		return errors.New("服务器身份或恢复命令已变更，不自动接管历史事务")
	}
	if len(record.Paths) != 3 || len(record.LockPaths) != 3 || len(e.Files) != 3 {
		return errors.New("部署事务目标集合不完整")
	}
	paths := make(map[string]bool)
	for _, path := range record.Paths {
		digest := e.Files[path]
		if paths[path] || RemotePath(path) != path || len(path) < 2 || path[0] != '/' ||
			!validDigest(digest) {
			return errors.New("部署事务路径或文件摘要无效")
		}
		paths[path] = true
	}
	locks := make(map[string]bool)
	for _, lock := range record.LockPaths {
		matched := false
		for path := range paths {
			matched = matched || lock == path+".scert-lock"
		}
		if !matched || locks[lock] {
			return errors.New("部署事务锁集合无效")
		}
		locks[lock] = true
	}
	temporaryPaths := make(map[string]bool)
	for _, temporary := range record.TemporaryPaths {
		matched := false
		for path := range paths {
			matched = matched || temporary == path+".scert-tmp-"+record.ID
		}
		if !matched || temporaryPaths[temporary] {
			return errors.New("部署事务临时路径无效")
		}
		temporaryPaths[temporary] = true
	}
	for path, digest := range e.TemporaryFiles {
		if !temporaryPaths[path] || !validDigest(digest) {
			return errors.New("部署事务临时文件归属证据无效")
		}
	}
	seen := make(map[string]bool)
	for _, backup := range record.Backups {
		if !paths[backup.Original] || seen[backup.Original] || backup.Saved != backup.Original+".scert-backup-"+record.ID ||
			(backup.OriginalExists && !validDigest(backup.OriginalDigest)) {
			return errors.New("部署事务备份身份无效")
		}
		seen[backup.Original] = true
	}
	return nil
}
