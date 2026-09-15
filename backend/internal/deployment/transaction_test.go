package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDeployLeavesExistingLockAndCertificateUntouched(t *testing.T) {
	client := newMemoryClient()
	server := testServer("locked")
	lockPath := server.PrivateKeyPath + ".scert-lock"
	client.files[lockPath] = memoryFile{data: []byte("other-transaction")}
	client.files[server.CertificatePath] = memoryFile{data: []byte("current-certificate")}
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})
	report := service.Deploy(t.Context(), []Target{{Server: server}}, testArtifact(t))
	if report.Status != "failed" || !strings.Contains(report.Results[0].Message, "部署锁") {
		t.Fatalf("已有部署锁时必须停止：%+v", report)
	}
	if len(client.files) != 2 || string(client.files[lockPath].data) != "other-transaction" || string(client.files[server.CertificatePath].data) != "current-certificate" || len(client.commands) != 0 {
		t.Fatal("锁冲突时不得覆盖证书、移除他人的锁或遗留自己取得的锁")
	}
}

type collisionClient struct {
	*memoryClient
	collided string
}

func (c *collisionClient) Upload(_ context.Context, path string, _ []byte, _ uint32) error {
	c.collided = path
	c.files[path] = memoryFile{data: []byte("not-owned")}
	return &uploadNotCreatedError{err: errors.New("测试独占创建冲突")}
}

type fixedClientFactory struct{ client Client }

func (f fixedClientFactory) Connect(context.Context, Server) (Client, error) { return f.client, nil }

func TestDeployDoesNotRemoveUnownedUploadPath(t *testing.T) {
	client := &collisionClient{memoryClient: newMemoryClient()}
	server := testServer("collision")
	client.files[server.CertificatePath] = memoryFile{data: []byte("current")}
	report := NewService(fixedClientFactory{client}).Deploy(t.Context(), []Target{{Server: server}}, testArtifact(t))
	if report.Status != "failed" || !strings.Contains(report.Results[0].Message, "所有权") {
		t.Fatalf("必须报告所有权不明确：%+v", report)
	}
	if string(client.files[client.collided].data) != "not-owned" || string(client.files[server.CertificatePath].data) != "current" || len(client.files) != 2 {
		t.Fatal("上传冲突不得删除原有临时文件或修改当前证书")
	}
}

type lostRenameClient struct {
	*memoryClient
	lostBackup bool
	lost       bool
	closed     bool
}

func (c *lostRenameClient) Disconnected() bool { return c.closed }

func (c *lostRenameClient) Rename(ctx context.Context, source, target string) error {
	if err := c.memoryClient.Rename(ctx, source, target); err != nil {
		return err
	}
	isBackup := strings.Contains(target, ".scert-backup-")
	isInstall := strings.Contains(source, ".scert-tmp-")
	if !c.lost && ((c.lostBackup && isBackup) || (!c.lostBackup && isInstall)) {
		c.lost, c.closed = true, true
		return context.DeadlineExceeded
	}
	return nil
}

type reconnectingMemoryFactory struct {
	client *lostRenameClient
	calls  int
}

func (f *reconnectingMemoryFactory) Connect(context.Context, Server) (Client, error) {
	f.calls++
	f.client.closed = false
	return f.client, nil
}

func TestDeployRecoversWhenRenameSucceededButReplyWasLost(t *testing.T) {
	for _, backup := range []bool{true, false} {
		t.Run(map[bool]string{true: "backup", false: "install"}[backup], func(t *testing.T) {
			client := &lostRenameClient{memoryClient: newMemoryClient(), lostBackup: backup}
			server := testServer("lost-reply")
			for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
				client.files[path] = memoryFile{data: []byte("original"), mode: 0o600}
			}
			factory := &reconnectingMemoryFactory{client: client}
			report := NewService(factory).Deploy(t.Context(), []Target{{Server: server}}, testArtifact(t))
			if report.Status != "failed" || !strings.Contains(report.Results[0].Message, "文件已回滚") || factory.calls != 2 {
				t.Fatalf("响应丢失后应重连并回滚：%+v，连接次数 %d", report, factory.calls)
			}
			if len(client.files) != 3 || len(client.commands) != 0 {
				t.Fatal("回滚后不得残留临时文件或锁，也不得执行 reload")
			}
			for _, file := range client.files {
				if string(file.data) != "original" || file.mode != 0o600 {
					t.Fatal("响应丢失后原始文件内容或权限被破坏")
				}
			}
		})
	}
}
