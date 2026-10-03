package deployment

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
)

var (
	ErrRecoveryInspectionUnavailable = errors.New("部署现场核查不可用")
	ErrRecoveryInspectionRecord      = errors.New("部署现场核查记录无效或不存在")
	ErrRecoveryInspectionIdentity    = errors.New("服务器身份与历史事务不一致，拒绝核查")
	ErrRecoveryInspectionRead        = errors.New("部署现场读取失败，不能认定核查完成")
)

// RecoveryInspection 仅为只读采样，不是原子快照，也不是提交或回滚的授权凭据。
type RecoveryInspection struct {
	TransactionID      string                    `json:"transaction_id"`
	ServerID           string                    `json:"server_id"`
	Phase              string                    `json:"phase"`
	Status             string                    `json:"status"`
	HistoricalEvidence bool                      `json:"historical_evidence"`
	ObservedAt         time.Time                 `json:"observed_at"`
	Files              []RecoveryFileObservation `json:"files"`
	Locks              []RecoveryLockObservation `json:"locks"`
}

type RecoveryFileObservation struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Exists     bool   `json:"exists"`
	Digest     string `json:"digest,omitempty"`
	Comparison string `json:"comparison"`
}

type RecoveryLockObservation struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Ownership string `json:"ownership"`
}

// InspectPending 只读取待恢复记录与远端文件，绝不运行命令、建立锁或修改事务。
// 缺失的历史证据保持缺失，当前观测摘要不能补写为当时的恢复证据。
func (s *Service) InspectPending(ctx context.Context, id string, resolve func(context.Context, string) (Server, error)) (RecoveryInspection, error) {
	if err := ctx.Err(); err != nil {
		return RecoveryInspection{}, err
	}
	if s == nil || s.journal == nil || s.factory == nil || resolve == nil {
		return RecoveryInspection{}, ErrRecoveryInspectionUnavailable
	}
	if !validInspectionID(id) {
		return RecoveryInspection{}, ErrRecoveryInspectionRecord
	}
	records, err := s.journal.ListPendingDeploymentTransactions(ctx)
	if err != nil {
		return RecoveryInspection{}, ErrRecoveryInspectionUnavailable
	}
	var record *TransactionRecord
	for i := range records {
		if records[i].ID == id {
			if record != nil {
				return RecoveryInspection{}, ErrRecoveryInspectionRecord
			}
			record = &records[i]
		}
	}
	if record == nil || validateInspectionRecord(*record) != nil {
		return RecoveryInspection{}, ErrRecoveryInspectionRecord
	}
	if err := ctx.Err(); err != nil {
		return RecoveryInspection{}, err
	}
	server, err := resolve(ctx, record.ServerID)
	if err != nil {
		return RecoveryInspection{}, ErrRecoveryInspectionUnavailable
	}
	server = server.Normalize()
	if server.ID != record.ServerID || server.Validate() != nil {
		return RecoveryInspection{}, ErrRecoveryInspectionIdentity
	}
	if record.Evidence != nil && validateRecoveryEvidence(*record, server) != nil {
		return RecoveryInspection{}, ErrRecoveryInspectionIdentity
	}
	if err := ctx.Err(); err != nil {
		return RecoveryInspection{}, err
	}
	client, err := s.factory.Connect(ctx, server)
	if err != nil {
		return RecoveryInspection{}, ErrRecoveryInspectionUnavailable
	}
	defer client.Close()
	reader, ok := client.(EvidenceClient)
	if !ok {
		return RecoveryInspection{}, ErrRecoveryInspectionUnavailable
	}
	result := RecoveryInspection{
		TransactionID: record.ID, ServerID: record.ServerID, Phase: record.Phase, Status: record.Status,
		HistoricalEvidence: record.Evidence != nil,
		Files:              make([]RecoveryFileObservation, 0, 9), Locks: make([]RecoveryLockObservation, 0, 3),
	}
	for _, target := range record.Paths {
		paths := []struct{ path, kind, expected string }{
			{target, "target", ""},
			{target + ".scert-tmp-" + record.ID, "temporary", ""},
			{target + ".scert-backup-" + record.ID, "backup", ""},
		}
		if record.Evidence != nil {
			paths[0].expected = record.Evidence.Files[target]
			paths[1].expected = record.Evidence.TemporaryFiles[paths[1].path]
			for _, backup := range record.Backups {
				if backup.Original == target && backup.OriginalExists {
					paths[2].expected = backup.OriginalDigest
				}
			}
		}
		for _, item := range paths {
			if err := ctx.Err(); err != nil {
				return RecoveryInspection{}, err
			}
			exists, err := client.Exists(ctx, item.path)
			if err != nil {
				return RecoveryInspection{}, ErrRecoveryInspectionRead
			}
			observation := RecoveryFileObservation{Path: item.path, Kind: item.kind, Exists: exists, Comparison: "missing"}
			if exists {
				digest, err := reader.FileDigest(ctx, item.path)
				if err != nil || !validDigest(digest) {
					return RecoveryInspection{}, ErrRecoveryInspectionRead
				}
				observation.Digest, observation.Comparison = digest, "unrecorded"
				if validDigest(item.expected) {
					observation.Comparison = "mismatch"
					if digest == item.expected {
						observation.Comparison = "match"
					}
				}
			}
			result.Files = append(result.Files, observation)
		}
		lock := RecoveryLockObservation{Path: target + ".scert-lock", Ownership: "missing"}
		lock.Exists, err = client.Exists(ctx, lock.Path)
		if err != nil {
			return RecoveryInspection{}, ErrRecoveryInspectionRead
		}
		if lock.Exists {
			owner, err := reader.ReadLockOwner(ctx, lock.Path)
			if err != nil {
				return RecoveryInspection{}, ErrRecoveryInspectionRead
			}
			lock.Ownership = "unproven"
			if record.Evidence != nil && validDigest(owner) {
				lock.Ownership = "foreign"
				if owner == record.Evidence.LockToken {
					lock.Ownership = "match"
				}
			}
		}
		result.Locks = append(result.Locks, lock)
	}
	if err := ctx.Err(); err != nil {
		return RecoveryInspection{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

func validInspectionID(id string) bool {
	if len(id) == 0 || len(id) > 160 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '.' && c != '_' {
			return false
		}
	}
	return id != "." && id != ".."
}

func validateInspectionRecord(record TransactionRecord) error {
	if !validInspectionID(record.ID) || record.ServerID == "" || len(record.Paths) != 3 ||
		(record.Status != TransactionStatusPending && record.Status != TransactionStatusRecoveryRequired) {
		return ErrRecoveryInspectionRecord
	}
	allowed := make(map[string]bool, 12)
	for _, target := range record.Paths {
		if len(target) < 2 || len(target) > 4096 || target[0] != '/' || RemotePath(target) != target ||
			strings.ContainsFunc(target, unicode.IsControl) {
			return ErrRecoveryInspectionRecord
		}
		for _, name := range []string{target, target + ".scert-lock", target + ".scert-tmp-" + record.ID, target + ".scert-backup-" + record.ID} {
			for previous := range allowed {
				if previous == name || strings.HasPrefix(name, previous+"/") || strings.HasPrefix(previous, name+"/") {
					return ErrRecoveryInspectionRecord
				}
			}
			allowed[name] = true
		}
	}
	return nil
}
