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
	ServerID string `json:"server_id"`
	Server   string `json:"server"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	BackupID string `json:"backup_id,omitempty"`
}

const deploymentRollbackTimeout = 30 * time.Second

type Report struct {
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Status  string   `json:"status"`
	Results []Result `json:"results"`
}

type Service struct {
	factory ClientFactory
	now     func() time.Time
}

func NewService(factory ClientFactory) *Service {
	return &Service{factory: factory, now: time.Now}
}

func (s *Service) Deploy(ctx context.Context, targets []Target, artifact Artifact) Report {
	report := Report{Total: len(targets), Results: make([]Result, 0, len(targets))}
	if s == nil || s.factory == nil {
		report.Status = "failed"
		report.Failed = len(targets)
		for _, target := range targets {
			report.Results = append(report.Results, Result{ServerID: target.Server.ID, Server: target.Server.Name, Status: "failed", Message: "部署服务未初始化"})
		}
		return report
	}
	if err := validateArtifact(artifact); err != nil {
		report.Status = "failed"
		report.Failed = len(targets)
		for _, target := range targets {
			report.Results = append(report.Results, Result{ServerID: target.Server.ID, Server: target.Server.Name, Status: "failed", Message: err.Error()})
		}
		return report
	}
	for _, target := range targets {
		result := s.deployOne(ctx, target.Server, artifact)
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
	certificateData, err := os.ReadFile(saved.CertificatePath)
	if err != nil {
		return Report{}, fmt.Errorf("读取证书文件失败：%w", err)
	}
	fullChainData, err := os.ReadFile(saved.FullChainPath)
	if err != nil {
		return Report{}, fmt.Errorf("读取完整证书链失败：%w", err)
	}
	privateKeyData, err := os.ReadFile(saved.PrivateKeyPath)
	if err != nil {
		return Report{}, fmt.Errorf("读取私钥文件失败：%w", err)
	}
	return s.Deploy(ctx, targets, Artifact{Certificate: certificateData, FullChain: fullChainData, PrivateKey: privateKeyData}), nil
}

func (s *Service) deployOne(ctx context.Context, server Server, artifact Artifact) (result Result) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	server = server.Normalize()
	result = Result{ServerID: server.ID, Server: server.Name, Status: "failed"}
	if err := server.Validate(); err != nil {
		result.Message = err.Error()
		return result
	}
	client, err := s.factory.Connect(ctx, server)
	if err != nil {
		result.Message = fmt.Sprintf("连接服务器失败：%v", err)
		return result
	}
	defer func() { _ = client.Close() }()
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
			result.Message = fmt.Sprintf("创建远程目录失败：%v", err)
			return result
		}
		lockPaths = append(lockPaths, file.path+".scert-lock")
	}
	sort.Strings(lockPaths)
	heldLocks := make([]string, 0, len(lockPaths))
	retainLocks := false
	defer func() {
		if len(heldLocks) == 0 || retainLocks {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
		defer cancel()
		if err := reconnectDeploymentClient(releaseCtx, s.factory, server, &client); err != nil {
			result.Status = "failed"
			result.Message += fmt.Sprintf("；释放部署锁失败，需人工检查：%v", err)
			return
		}
		for index := len(heldLocks) - 1; index >= 0; index-- {
			if err := client.Unlock(releaseCtx, heldLocks[index]); err != nil {
				result.Status = "failed"
				result.Message += fmt.Sprintf("；释放部署锁 %s 失败，需人工检查：%v", heldLocks[index], err)
			}
		}
	}()
	for _, lockPath := range lockPaths {
		if err := client.Lock(ctx, lockPath); err != nil {
			result.Message = fmt.Sprintf("无法取得部署锁 %s，可能存在并发部署或遗留锁；未修改当前证书，请人工核查：%v", lockPath, err)
			return result
		}
		heldLocks = append(heldLocks, lockPath)
	}
	backupID := s.backupID()
	result.BackupID = backupID
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
			message += fmt.Sprintf("；自动回滚失败：%v", rollbackErr)
			retainLocks = true
			message += "；已保留本次部署锁，修复并确认文件状态后再由管理员释放"
		} else {
			message += "；文件已回滚"
		}
		if reloadAttempted {
			// 命令可能已部分执行；文件回滚并不证明运行中的服务已经恢复。
			message += "；服务运行状态未确认，请检查服务并在确认旧配置后重新加载"
		}
		result.Message = message
		return result
	}
	for _, file := range paths {
		temporaryPath := file.path + ".scert-tmp-" + backupID
		// 上传失败也可能留下半份私钥，必须在开始上传前登记清理路径。
		temporary = append(temporary, temporaryPath)
		if err := client.Upload(ctx, temporaryPath, file.data, file.mode); err != nil {
			var notCreated *uploadNotCreatedError
			if errors.As(err, &notCreated) {
				temporary = temporary[:len(temporary)-1]
				return failed(fmt.Sprintf("上传失败且未确认临时路径所有权，已保留 %s 供人工核查：%v", temporaryPath, err))
			}
			return failed(fmt.Sprintf("上传远程证书失败：%v", err))
		}
	}
	// 所有文件上传完成后才触碰当前版本，避免上传失败造成不必要的证书替换。
	for _, file := range paths {
		exists, err := client.Exists(ctx, file.path)
		if err != nil {
			return failed(fmt.Sprintf("检查远程文件失败：%v", err))
		}
		if exists {
			backupPath := file.path + ".scert-backup-" + backupID
			if exists, err := client.Exists(ctx, backupPath); err != nil || exists {
				return failed(fmt.Sprintf("备份目标已存在或无法检查，已停止替换：%s：%v", backupPath, err))
			}
			backups = append(backups, backupFile{original: file.path, saved: backupPath})
			if err := client.Rename(ctx, file.path, backupPath); err != nil {
				return failed(fmt.Sprintf("备份远程证书失败：%v", err))
			}
		}
		temporaryPath := file.path + ".scert-tmp-" + backupID
		installed = append(installed, file.path)
		if err := client.Rename(ctx, temporaryPath, file.path); err != nil {
			return failed(fmt.Sprintf("替换远程证书失败：%v", err))
		}
	}
	validationResult, err := client.Run(ctx, server.ValidationCommand)
	if err != nil || validationResult.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("退出码 %d", validationResult.ExitCode)
		}
		return failed(fmt.Sprintf("配置校验失败：%v", err))
	}
	reloadAttempted = true
	commandResult, err := client.Run(ctx, server.ReloadCommand)
	if err != nil || commandResult.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("退出码 %d", commandResult.ExitCode)
		}
		return failed(fmt.Sprintf("reload 失败：%v", err))
	}
	if err := cleanupBackups(ctx, client, backups, paths); err != nil {
		result.Status = "failed"
		result.Message = fmt.Sprintf("\u8bc1\u4e66\u5df2\u90e8\u7f72\u5e76 reload \u6210\u529f\uff0c\u4f46\u65e7\u8bc1\u4e66\u5907\u4efd\u6e05\u7406\u5931\u8d25\uff1a%v", err)
		return result
	}
	result.Status = "success"
	result.Message = "证书部署、配置校验并 reload 成功"
	result.BackupID = backupID
	return result
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
