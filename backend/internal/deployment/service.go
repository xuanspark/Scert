package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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

func (s *Service) deployOne(ctx context.Context, server Server, artifact Artifact) Result {
	server = server.Normalize()
	result := Result{ServerID: server.ID, Server: server.Name, Status: "failed"}
	if err := server.Validate(); err != nil {
		result.Message = err.Error()
		return result
	}
	client, err := s.factory.Connect(ctx, server)
	if err != nil {
		result.Message = fmt.Sprintf("连接服务器失败：%v", err)
		return result
	}
	defer client.Close()
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
	backupID := s.backupID()
	type backup struct{ original, saved string }
	backups := make([]backup, 0, len(paths))
	temporary := make([]string, 0, len(paths))
	installed := make([]string, 0, len(paths))
	rollback := func() {
		for _, path := range temporary {
			_ = client.Remove(context.Background(), path)
		}
		for _, path := range installed {
			_ = client.Remove(context.Background(), path)
		}
		for index := len(backups) - 1; index >= 0; index-- {
			item := backups[index]
			_ = client.Rename(context.Background(), item.saved, item.original)
		}
	}
	for _, file := range paths {
		if err := client.MkdirAll(ctx, parentPath(file.path)); err != nil {
			result.Message = fmt.Sprintf("创建远程目录失败：%v", err)
			rollback()
			return result
		}
		exists, err := client.Exists(ctx, file.path)
		if err != nil {
			result.Message = fmt.Sprintf("检查远程文件失败：%v", err)
			rollback()
			return result
		}
		if exists {
			backupPath := file.path + ".scert-backup-" + backupID
			if err := client.Rename(ctx, file.path, backupPath); err != nil {
				result.Message = fmt.Sprintf("备份远程证书失败：%v", err)
				rollback()
				return result
			}
			backups = append(backups, backup{original: file.path, saved: backupPath})
		}
		temporaryPath := file.path + ".scert-tmp-" + backupID
		if err := client.Upload(ctx, temporaryPath, file.data, file.mode); err != nil {
			result.Message = fmt.Sprintf("上传远程证书失败：%v", err)
			rollback()
			return result
		}
		temporary = append(temporary, temporaryPath)
		if err := client.Rename(ctx, temporaryPath, file.path); err != nil {
			result.Message = fmt.Sprintf("替换远程证书失败：%v", err)
			rollback()
			return result
		}
		temporary = temporary[:len(temporary)-1]
		installed = append(installed, file.path)
	}
	commandResult, err := client.Run(ctx, server.ReloadCommand)
	if err != nil || commandResult.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("退出码 %d", commandResult.ExitCode)
		}
		result.Message = fmt.Sprintf("reload 失败：%v", err)
		rollback()
		return result
	}
	result.Status = "success"
	result.Message = "证书部署并 reload 成功"
	result.BackupID = backupID
	return result
}

type remoteFile struct {
	path string
	data []byte
	mode uint32
}

func validateArtifact(artifact Artifact) error {
	if len(artifact.Certificate) == 0 || len(artifact.FullChain) == 0 || len(artifact.PrivateKey) == 0 {
		return errors.New("证书部署文件不完整")
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
