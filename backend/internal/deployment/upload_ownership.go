package deployment

import (
	"context"
	"fmt"
)

// captureUploadEvidence 只记录已确认独占创建的文件，计划路径不构成所有权证据。
// 创建响应丢失、摘要读取失败或证据未落库时，重启恢复必须保留未知文件。
func (s *Service) captureUploadEvidence(ctx context.Context, server Server, client *Client, record *TransactionRecord, path string, data []byte, uploadErr error) error {
	evidenceContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
	defer cancel()
	digest := contentDigest(data)
	if uploadErr != nil {
		if err := reconnectDeploymentClient(evidenceContext, s.factory, server, client); err != nil {
			return fmt.Errorf("读取部分上传证据前重连失败：%w", err)
		}
		reader, ok := (*client).(EvidenceClient)
		if !ok {
			return fmt.Errorf("客户端无法核验部分上传文件")
		}
		var err error
		digest, err = reader.FileDigest(evidenceContext, path)
		if err != nil {
			return fmt.Errorf("读取部分上传摘要失败：%w", err)
		}
		if !validDigest(digest) {
			return fmt.Errorf("部分上传摘要无效")
		}
	}
	if record.Evidence.TemporaryFiles == nil {
		record.Evidence.TemporaryFiles = make(map[string]string)
	}
	record.Evidence.TemporaryFiles[path] = digest
	record.UpdatedAt = s.now().UTC()
	return s.journal.UpdateDeploymentTransaction(evidenceContext, *record)
}

// validateTemporaryFiles 在删除任何临时文件前完整检查实际归属及内容身份。
// 旧日志只有计划路径而没有创建证据时，不自动删除仍存在的文件。
func validateTemporaryFiles(ctx context.Context, client Client, record TransactionRecord) error {
	for _, path := range record.TemporaryPaths {
		exists, err := client.Exists(ctx, path)
		if err != nil {
			return fmt.Errorf("检查临时文件失败：%w", err)
		}
		if !exists {
			continue
		}
		if record.Evidence == nil || !validDigest(record.Evidence.TemporaryFiles[path]) {
			return fmt.Errorf("无法证明临时文件归属，已保留现场：%s", path)
		}
		if err := checkFileDigest(ctx, client, path, record.Evidence.TemporaryFiles[path]); err != nil {
			return fmt.Errorf("临时文件身份已变更，已保留现场：%w", err)
		}
	}
	return nil
}
