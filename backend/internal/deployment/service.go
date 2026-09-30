package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type Target struct {
	Server Server
}

type Artifact struct {
	Certificate []byte
	FullChain   []byte
	PrivateKey  []byte
}

type SavedFiles struct {
	CertificatePath string
	FullChainPath   string
	PrivateKeyPath  string
}

type Result struct {
	ServerID    string      `json:"server_id"`
	Server      string      `json:"server"`
	Status      string      `json:"status"`
	Message     string      `json:"message"`
	BackupID    string      `json:"backup_id,omitempty"`
	FailureCode FailureCode `json:"failure_code,omitempty"`
}

const deploymentRollbackTimeout = 30 * time.Second

type Report struct {
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Status  string   `json:"status"`
	Results []Result `json:"results"`
	// 站点校验与各服务器文件推送分开记录，不表示所有服务器均完成独立 HTTPS 验证。
	VerificationStatus string `json:"verification_status,omitempty"`
	RecoveryStatus     string `json:"recovery_status,omitempty"`
}

type Service struct {
	factory ClientFactory
	journal TransactionJournal
	now     func() time.Time
}

func NewService(factory ClientFactory) *Service {
	return &Service{factory: factory, now: time.Now}
}

func NewServiceWithJournal(factory ClientFactory, journal TransactionJournal) *Service {
	return &Service{factory: factory, journal: journal, now: time.Now}
}

func (s *Service) Deploy(ctx context.Context, targets []Target, artifact Artifact) Report {
	return s.deploy(ctx, targets, artifact, nil)
}

func (s *Service) deploy(ctx context.Context, targets []Target, artifact Artifact, pending *[]*pendingDeployment) Report {
	report := Report{Total: len(targets), Results: make([]Result, 0, len(targets))}
	if len(targets) == 0 {
		report.Status = "failed"
		return report
	}
	if s == nil || s.factory == nil {
		report.Status = "failed"
		report.Failed = len(targets)
		for _, target := range targets {
			report.Results = append(report.Results, Result{ServerID: target.Server.ID, Server: target.Server.Name, Status: "failed", Message: "部署服务未初始化", FailureCode: FailureUnavailable})
		}
		return report
	}
	if err := validateArtifact(artifact); err != nil {
		report.Status = "failed"
		report.Failed = len(targets)
		for _, target := range targets {
			report.Results = append(report.Results, Result{ServerID: target.Server.ID, Server: target.Server.Name, Status: "failed", Message: err.Error(), FailureCode: FailureArtifact})
		}
		return report
	}
	for index, target := range targets {
		result := s.deployOne(ctx, target.Server, artifact, pending, index)
		report.Results = append(report.Results, result)
		if result.Status == "success" {
			report.Success++
		} else {
			report.Failed++
		}
	}
	switch {
	case report.Failed == 0:
		report.Status = "success"
	case report.Success == 0:
		report.Status = "failed"
	default:
		report.Status = "partial"
	}
	return report
}

func (s *Service) DeploySaved(ctx context.Context, targets []Target, saved SavedFiles) (Report, error) {
	artifact, err := loadArtifact(saved)
	if err != nil {
		return Report{}, err
	}
	return s.Deploy(ctx, targets, artifact), nil
}

func loadArtifact(saved SavedFiles) (Artifact, error) {
	certificateData, err := os.ReadFile(saved.CertificatePath)
	if err != nil {
		return Artifact{}, fmt.Errorf("读取证书文件失败：%w", err)
	}
	fullChainData, err := os.ReadFile(saved.FullChainPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("读取完整证书链失败：%w", err)
	}
	privateKeyData, err := os.ReadFile(saved.PrivateKeyPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("读取私钥文件失败：%w", err)
	}
	return Artifact{Certificate: certificateData, FullChain: fullChainData, PrivateKey: privateKeyData}, nil
}

func (s *Service) deployOne(ctx context.Context, server Server, artifact Artifact, pending *[]*pendingDeployment, index int) (result Result) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	server = server.Normalize()
	result = Result{ServerID: server.ID, Server: server.Name, Status: "failed", FailureCode: FailureConfiguration}
	if err := server.Validate(); err != nil {
		result.Message = err.Error()
		return result
	}
	client, err := s.factory.Connect(ctx, server)
	if err != nil {
		result.FailureCode = FailureConnection
		result.Message = fmt.Sprintf("连接服务器失败：%v", err)
		return result
	}
	handedOff := false
	defer func() {
		if !handedOff {
			_ = client.Close()
		}
	}()
	paths := []remoteFile{
		{path: RemotePath(server.CertificatePath), data: artifact.Certificate, mode: 0o644},
		{path: RemotePath(server.CertificateChainPath), data: artifact.FullChain, mode: 0o644},
		{path: RemotePath(server.PrivateKeyPath), data: artifact.PrivateKey, mode: 0o600},
	}
	for _, file := range paths {
		if file.path == "" || file.path == "." {
			result.Message = "远程证书路径无效"
			return result
		}
	}
	if err := validateDeploymentPaths(paths); err != nil {
		result.Message = err.Error()
		return result
	}
	// 每个实际文件路径使用远程协作锁，阻止不同任务或进程的替换与回滚相互覆盖。
	lockPaths := make([]string, 0, len(paths))
	for _, file := range paths {
		if err := client.MkdirAll(ctx, parentPath(file.path)); err != nil {
			result.FailureCode = FailureDirectory
			result.Message = fmt.Sprintf("创建远程目录失败：%v", err)
			return result
		}
		lockPaths = append(lockPaths, file.path+".scert-lock")
	}
	sort.Strings(lockPaths)
	heldLocks := make([]string, 0, len(lockPaths))
	retainLocks := false
	releaseLocks := func() {
		if len(heldLocks) == 0 || retainLocks {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
		defer cancel()
		if err := reconnectDeploymentClient(releaseCtx, s.factory, server, &client); err != nil {
			result.Status = "failed"
			result.FailureCode = FailureLockRelease
			result.Message += fmt.Sprintf("；释放部署锁失败，需人工检查：%v", err)
			return
		}
		released := true
		for index := len(heldLocks) - 1; index >= 0; index-- {
			if err := client.Unlock(releaseCtx, heldLocks[index]); err != nil {
				released = false
				result.Status = "failed"
				result.FailureCode = FailureLockRelease
				result.Message += fmt.Sprintf("；释放部署锁 %s 失败，需人工检查：%v", heldLocks[index], err)
			}
		}
		if released {
			heldLocks = nil
		}
	}
	defer func() {
		if !handedOff {
			releaseLocks()
		}
	}()
	for _, lockPath := range lockPaths {
		if err := client.Lock(ctx, lockPath); err != nil {
			result.FailureCode = FailureLock
			result.Message = fmt.Sprintf("无法取得部署锁 %s，可能存在并发部署或遗留锁；未修改当前证书，请人工核查：%v", lockPath, err)
			return result
		}
		heldLocks = append(heldLocks, lockPath)
	}
	backupID := s.backupID()
	result.BackupID = backupID
	journalRecord, transactionEnabled := s.newTransactionRecord(ctx, server, paths, lockPaths, backupID, index)
	if transactionEnabled {
		if err := s.journal.CreateDeploymentTransaction(ctx, *journalRecord); err != nil {
			result.FailureCode = FailureUnavailable
			result.Message = fmt.Sprintf("保存部署事务现场失败：%v", err)
			return result
		}
	}
	persistTransaction := func(phase string) error {
		if !transactionEnabled {
			return nil
		}
		journalRecord.Phase = phase
		journalRecord.UpdatedAt = s.now().UTC()
		return s.journal.UpdateDeploymentTransaction(ctx, *journalRecord)
	}
	finishTransaction := func(status, message string) {
		if !transactionEnabled {
			return
		}
		if err := s.journal.FinishDeploymentTransaction(context.WithoutCancel(ctx), journalRecord.ID, status, message); err != nil {
			result.Status = "failed"
			result.FailureCode = FailureRecoveryUnknown
			result.Message += fmt.Sprintf("；保存部署事务最终状态失败：%v", err)
		}
	}
	backups := make([]backupFile, 0, len(paths))
	temporary := make([]string, 0, len(paths))
	installed := make([]string, 0, len(paths))
	rollback := func() error {
		rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
		defer cancel()
		if err := reconnectDeploymentClient(rollbackContext, s.factory, server, &client); err != nil {
			return fmt.Errorf("重新连接服务器以回滚失败：%w", err)
		}
		var rollbackErrors []error
		for _, path := range temporary {
			if err := client.Remove(rollbackContext, path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("删除临时文件 %s 失败：%w", path, err))
			}
		}
		for _, path := range installed {
			if err := client.Remove(rollbackContext, path); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("删除新证书文件 %s 失败：%w", path, err))
			}
		}
		for index := len(backups) - 1; index >= 0; index-- {
			item := backups[index]
			// 备份请求超时可能丢失响应，重连后以目标文件是否存在核实实际结果。
			exists, err := client.Exists(rollbackContext, item.saved)
			if err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("检查备份文件 %s 失败：%w", item.saved, err))
				continue
			}
			if !exists {
				continue
			}
			if err := client.Rename(rollbackContext, item.saved, item.original); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("恢复备份文件 %s 失败：%w", item.original, err))
			}
		}
		return errors.Join(rollbackErrors...)
	}
	reloadAttempted := false
	failed := func(message string) Result {
		if rollbackErr := rollback(); rollbackErr != nil {
			result.FailureCode = FailureRollback
			message += fmt.Sprintf("；自动回滚失败：%v", rollbackErr)
			retainLocks = true
			message += "；已保留本次部署锁，修复并确认文件状态后再由管理员释放"
		} else {
			message += "；文件已回滚"
		}
		if reloadAttempted {
			if !retainLocks {
				result.FailureCode = FailureRuntimeUnknown
			}
			// 命令可能已部分执行；文件回滚并不证明运行中的服务已经恢复。
			message += "；服务运行状态未确认，请检查服务并在确认旧配置后重新加载"
		}
		result.Message = message
		releaseLocks()
		if retainLocks || result.FailureCode == FailureLockRelease {
			finishTransaction(TransactionStatusRecoveryRequired, result.Message)
		} else {
			finishTransaction(TransactionStatusRolledBack, result.Message)
		}
		return result
	}
	result.FailureCode = FailureUpload
	for _, file := range paths {
		temporaryPath := file.path + ".scert-tmp-" + backupID
		// 上传失败也可能留下半份私钥，必须在开始上传前登记清理路径。
		temporary = append(temporary, temporaryPath)
		if transactionEnabled {
			journalRecord.TemporaryPaths = append(journalRecord.TemporaryPaths, temporaryPath)
		}
		if err := client.Upload(ctx, temporaryPath, file.data, file.mode); err != nil {
			var notCreated *uploadNotCreatedError
			if errors.As(err, &notCreated) {
				temporary = temporary[:len(temporary)-1]
				return failed(fmt.Sprintf("上传失败且未确认临时路径所有权，已保留 %s 供人工核查：%v", temporaryPath, err))
			}
			return failed(fmt.Sprintf("上传远程证书失败：%v", err))
		}
	}
	if err := persistTransaction(TransactionPhaseUploaded); err != nil {
		return failed(fmt.Sprintf("保存上传阶段失败：%v", err))
	}
	// 所有文件上传完成后才触碰当前版本，避免上传失败造成不必要的证书替换。
	result.FailureCode = FailureReplace
	for _, file := range paths {
		exists, err := client.Exists(ctx, file.path)
		if err != nil {
			return failed(fmt.Sprintf("检查远程文件失败：%v", err))
		}
		backupPath := file.path + ".scert-backup-" + backupID
		if backupExists, checkErr := client.Exists(ctx, backupPath); checkErr != nil || backupExists {
			return failed(fmt.Sprintf("备份目标已存在或无法检查，已停止替换：%s：%v", backupPath, checkErr))
		}
		if transactionEnabled {
			journalRecord.Backups = append(journalRecord.Backups, TransactionBackup{Original: file.path, Saved: backupPath, OriginalExists: exists})
			if err := persistTransaction(TransactionPhaseReplacing); err != nil {
				return failed(fmt.Sprintf("保存替换阶段失败：%v", err))
			}
		}
		if exists {
			backups = append(backups, backupFile{original: file.path, saved: backupPath})
			if err := client.Rename(ctx, file.path, backupPath); err != nil {
				return failed(fmt.Sprintf("备份远程证书失败：%v", err))
			}
			if err := persistTransaction(TransactionPhaseReplacing); err != nil {
				return failed(fmt.Sprintf("保存备份阶段失败：%v", err))
			}
		}
		temporaryPath := file.path + ".scert-tmp-" + backupID
		installed = append(installed, file.path)
		if transactionEnabled {
			journalRecord.InstalledPaths = append(journalRecord.InstalledPaths, file.path)
			if err := persistTransaction(TransactionPhaseReplacing); err != nil {
				return failed(fmt.Sprintf("保存安装阶段失败：%v", err))
			}
		}
		if err := client.Rename(ctx, temporaryPath, file.path); err != nil {
			return failed(fmt.Sprintf("替换远程证书失败：%v", err))
		}
		if err := persistTransaction(TransactionPhaseInstalled); err != nil {
			return failed(fmt.Sprintf("保存已安装阶段失败：%v", err))
		}
	}
	result.FailureCode = FailureValidation
	if err := persistTransaction(TransactionPhaseInstalled); err != nil {
		return failed(fmt.Sprintf("保存校验前阶段失败：%v", err))
	}
	validationResult, err := client.Run(ctx, server.ValidationCommand)
	if err != nil || validationResult.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("退出码 %d", validationResult.ExitCode)
		}
		return failed(fmt.Sprintf("配置校验失败：%v", err))
	}
	reloadAttempted = true
	if transactionEnabled {
		journalRecord.ReloadAttempted = true
		if err := persistTransaction(TransactionPhaseReloadAttempted); err != nil {
			return failed(fmt.Sprintf("保存重载前阶段失败：%v", err))
		}
	}
	commandResult, err := client.Run(ctx, server.ReloadCommand)
	if err != nil || commandResult.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("退出码 %d", commandResult.ExitCode)
		}
		return failed(fmt.Sprintf("reload 失败：%v", err))
	}
	if pending != nil {
		// 旧文件与锁在所有目标及站点校验结束前始终保留，禁止提前提交。
		result.Status = "success"
		result.FailureCode = ""
		result.Message = "证书已推送并重载，等待站点校验"
		closed := false
		transaction := &pendingDeployment{index: index}
		transaction.restore = func(recoveryContext context.Context) Result {
			result.Status = "failed"
			if err := rollback(); err != nil {
				result.FailureCode = FailureRollback
				result.Message = fmt.Sprintf("部署未提交，自动回滚失败：%v", err)
				return result
			}
			result.FailureCode = FailureRecoveryUnknown
			if err := reconnectDeploymentClient(recoveryContext, s.factory, server, &client); err != nil {
				result.Message = fmt.Sprintf("旧文件已恢复，重新连接以恢复服务失败：%v", err)
				return result
			}
			for _, command := range [][]string{server.ValidationCommand, server.ReloadCommand} {
				outcome, err := client.Run(recoveryContext, command)
				if err != nil || outcome.ExitCode != 0 {
					result.Message = fmt.Sprintf("旧文件已恢复，但恢复配置校验或重载失败，退出码 %d：%v", outcome.ExitCode, err)
					return result
				}
			}
			result.FailureCode = FailureRecoveryPending
			result.Message = "本次部署未提交，旧文件已恢复并通过配置校验及重载，等待站点恢复核验"
			return result
		}
		transaction.commit = func(commitContext context.Context) Result {
			if err := persistTransaction("committing"); err != nil {
				result.Status = "failed"
				result.FailureCode = FailureCleanup
				result.Message = fmt.Sprintf("保存提交阶段失败：%v", err)
				return result
			}
			if err := cleanupBackups(commitContext, client, backups, paths); err != nil {
				result.Status = "failed"
				result.FailureCode = FailureCleanup
				result.Message = fmt.Sprintf("站点校验通过，但事务旧文件清理失败：%v", err)
				return result
			}
			result.Message = "证书部署、配置校验并 reload 成功"
			return result
		}
		transaction.finish = func(retain bool) Result {
			if !closed {
				retainLocks = retain
				releaseLocks()
				_ = client.Close()
				closed = true
			}
			status := TransactionStatusCommitted
			if retain || result.Status != "success" {
				status = TransactionStatusRecoveryRequired
			} else if result.Message != "" && result.Status == "failed" {
				status = TransactionStatusRolledBack
			}
			finishTransaction(status, result.Message)
			return result
		}
		if transactionEnabled {
			journalRecord.Phase = TransactionPhaseReloadAttempted
		}
		handedOff = true
		*pending = append(*pending, transaction)
		return result
	}
	if err := cleanupBackups(ctx, client, backups, paths); err != nil {
		result.Status = "failed"
		result.FailureCode = FailureCleanup
		result.Message = fmt.Sprintf("证书已部署并 reload 成功，但旧证书备份清理失败：%v", err)
		releaseLocks()
		finishTransaction(TransactionStatusRecoveryRequired, result.Message)
		return result
	}
	result.Status = "success"
	result.FailureCode = ""
	result.Message = "证书部署、配置校验并 reload 成功"
	result.BackupID = backupID
	releaseLocks()
	if result.FailureCode == FailureLockRelease {
		finishTransaction(TransactionStatusRecoveryRequired, result.Message)
	} else {
		finishTransaction(TransactionStatusCommitted, result.Message)
	}
	return result
}

func (s *Service) newTransactionRecord(ctx context.Context, server Server, paths []remoteFile, lockPaths []string, id string, index int) (*TransactionRecord, bool) {
	transactionContext, ok := TransactionContextFromContext(ctx)
	if !ok || s == nil || s.journal == nil {
		return nil, false
	}
	pathValues := make([]string, 0, len(paths))
	temporaryPaths := make([]string, 0, len(paths))
	for _, file := range paths {
		pathValues = append(pathValues, string(file.path))
		temporaryPaths = append(temporaryPaths, string(file.path)+".scert-tmp-"+id)
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	return &TransactionRecord{
		ID: id, TaskID: transactionContext.TaskID, AttemptID: transactionContext.AttemptID,
		AttemptNumber: transactionContext.AttemptNumber, ResultIndex: index,
		ServerID: server.ID, ServerName: server.Name, Paths: pathValues,
		TemporaryPaths: temporaryPaths, LockPaths: append([]string(nil), lockPaths...),
		Phase: TransactionPhaseLocked, Status: TransactionStatusPending, UpdatedAt: now,
	}, true
}

// RecoverPending 在服务启动时只处理能够证明属于本次事务的现场；无法证明时保留现场并要求人工核查。
func (s *Service) RecoverPending(ctx context.Context, resolve func(context.Context, string) (Server, error)) error {
	if s == nil || s.journal == nil {
		return nil
	}
	if resolve == nil {
		return errors.New("部署事务恢复缺少服务器解析器")
	}
	records, err := s.journal.ListPendingDeploymentTransactions(ctx)
	if err != nil {
		return fmt.Errorf("读取待恢复部署事务失败：%w", err)
	}
	var recoveryErrors []error
	for _, record := range records {
		if err := s.recoverTransaction(ctx, record, resolve); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("事务 %s 恢复失败：%w", record.ID, err))
			record.Status = TransactionStatusRecoveryRequired
			record.Phase = TransactionStatusRecoveryRequired
			record.Message = err.Error()
			if updateErr := s.journal.UpdateDeploymentTransaction(context.WithoutCancel(ctx), record); updateErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("事务 %s 保存人工核查状态失败：%w", record.ID, updateErr))
			}
			continue
		}
		if err := s.journal.FinishDeploymentTransaction(context.WithoutCancel(ctx), record.ID, TransactionStatusRolledBack, "服务启动已完成中断部署事务回滚"); err != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("事务 %s 保存恢复结果失败：%w", record.ID, err))
		}
	}
	return errors.Join(recoveryErrors...)
}

func (s *Service) recoverTransaction(ctx context.Context, record TransactionRecord, resolve func(context.Context, string) (Server, error)) error {
	server, err := resolve(ctx, record.ServerID)
	if err != nil {
		return fmt.Errorf("读取服务器配置失败：%w", err)
	}
	server = server.Normalize()
	if err := server.Validate(); err != nil {
		return fmt.Errorf("服务器配置无效：%w", err)
	}
	recoveryContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
	defer cancel()
	client, err := s.factory.Connect(recoveryContext, server)
	if err != nil {
		return fmt.Errorf("连接服务器失败：%w", err)
	}
	defer client.Close()
	for _, lockPath := range record.LockPaths {
		exists, checkErr := client.Exists(recoveryContext, lockPath)
		if checkErr != nil || !exists {
			if checkErr == nil {
				checkErr = errors.New("部署锁不存在")
			}
			return fmt.Errorf("无法证明事务仍拥有部署锁 %s：%w", lockPath, checkErr)
		}
	}
	for _, temporaryPath := range record.TemporaryPaths {
		if exists, checkErr := client.Exists(recoveryContext, temporaryPath); checkErr != nil {
			return fmt.Errorf("检查临时文件失败：%w", checkErr)
		} else if exists {
			if err := client.Remove(recoveryContext, temporaryPath); err != nil {
				return fmt.Errorf("删除临时文件失败：%w", err)
			}
		}
	}
	for index := len(record.Backups) - 1; index >= 0; index-- {
		backup := record.Backups[index]
		backupExists, checkErr := client.Exists(recoveryContext, backup.Saved)
		if checkErr != nil {
			return fmt.Errorf("检查备份文件失败：%w", checkErr)
		}
		targetExists, checkErr := client.Exists(recoveryContext, backup.Original)
		if checkErr != nil {
			return fmt.Errorf("检查目标文件失败：%w", checkErr)
		}
		if backup.OriginalExists {
			if !backupExists {
				if targetExists {
					continue
				}
				return fmt.Errorf("备份文件和目标文件均不存在：%s", backup.Original)
			}
			if targetExists {
				if err := client.Remove(recoveryContext, backup.Original); err != nil {
					return fmt.Errorf("删除待恢复目标文件失败：%w", err)
				}
			}
			if err := client.Rename(recoveryContext, backup.Saved, backup.Original); err != nil {
				return fmt.Errorf("恢复备份文件失败：%w", err)
			}
			continue
		}
		if targetExists {
			if err := client.Remove(recoveryContext, backup.Original); err != nil {
				return fmt.Errorf("删除新增目标文件失败：%w", err)
			}
		}
	}
	if record.ReloadAttempted {
		for _, command := range [][]string{server.ValidationCommand, server.ReloadCommand} {
			result, runErr := client.Run(recoveryContext, command)
			if runErr != nil || result.ExitCode != 0 {
				if runErr == nil {
					runErr = fmt.Errorf("退出码 %d", result.ExitCode)
				}
				return fmt.Errorf("恢复服务失败：%w", runErr)
			}
		}
	}
	for index := len(record.LockPaths) - 1; index >= 0; index-- {
		if err := client.Unlock(recoveryContext, record.LockPaths[index]); err != nil {
			return fmt.Errorf("释放部署锁失败：%w", err)
		}
	}
	return nil
}

func cleanupBackups(ctx context.Context, client Client, backups []backupFile, paths []remoteFile) error {
	seen := make(map[string]struct{}, len(backups))
	for _, item := range backups {
		if _, exists := seen[item.saved]; exists {
			continue
		}
		seen[item.saved] = struct{}{}
		if err := client.Remove(ctx, item.saved); err != nil {
			return fmt.Errorf("\u5220\u9664\u672c\u6b21\u5907\u4efd %s \u5931\u8d25\uff1a%w", item.saved, err)
		}
	}
	for _, file := range paths {
		directory := parentPath(file.path)
		entries, err := client.List(ctx, directory)
		if err != nil {
			return fmt.Errorf("\u5217\u51fa\u8bc1\u4e66\u76ee\u5f55 %s \u5931\u8d25\uff1a%w", directory, err)
		}
		prefix := file.path + ".scert-backup-"
		for _, entry := range entries {
			if !strings.HasPrefix(entry, prefix) {
				continue
			}
			if _, exists := seen[entry]; exists {
				continue
			}
			seen[entry] = struct{}{}
			if err := client.Remove(ctx, entry); err != nil {
				return fmt.Errorf("\u5220\u9664\u5386\u53f2\u5907\u4efd %s \u5931\u8d25\uff1a%w", entry, err)
			}
		}
	}
	return nil
}
func reconnectDeploymentClient(ctx context.Context, factory ClientFactory, server Server, client *Client) error {
	if state, ok := (*client).(interface{ Disconnected() bool }); ok && state.Disconnected() {
		_ = (*client).Close()
		reconnected, err := factory.Connect(ctx, server)
		if err != nil {
			return err
		}
		*client = reconnected
	}
	return nil
}

type remoteFile struct {
	path string
	data []byte
	mode uint32
}

type backupFile struct {
	original string
	saved    string
}

func validateDeploymentPaths(files []remoteFile) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		cleaned := RemotePath(file.path)
		if cleaned == "" || cleaned == "." || !strings.HasPrefix(cleaned, "/") {
			return errors.New("远程证书路径必须是绝对路径")
		}
		if _, exists := seen[cleaned]; exists {
			return errors.New("远程证书、完整证书链和私钥不能使用相同路径")
		}
		seen[cleaned] = struct{}{}
	}
	return nil
}

func (s *Service) backupID() string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return now().UTC().Format("20060102-150405.000000000") + "-" + hex.EncodeToString(random[:])
}

func parentPath(path string) string {
	index := strings.LastIndexAny(path, "/\\")
	if index < 1 {
		return "."
	}
	return path[:index]
}
