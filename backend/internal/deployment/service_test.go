package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryFile struct {
	data []byte
	mode uint32
}

type memoryClient struct {
	files        map[string]memoryFile
	commands     [][]string
	runResults   []CommandResult
	runErrors    []error
	runIndex     int
	uploadError  string
	removeError  string
	renameError  string
	restoreError bool
}

func newMemoryClient() *memoryClient {
	return &memoryClient{files: map[string]memoryFile{}}
}

func (c *memoryClient) MkdirAll(context.Context, string) error { return nil }

func (c *memoryClient) Exists(_ context.Context, path string) (bool, error) {
	_, ok := c.files[path]
	return ok, nil
}

func (c *memoryClient) Upload(_ context.Context, path string, data []byte, mode uint32) error {
	if c.uploadError != "" && strings.Contains(path, c.uploadError) {
		return errors.New("模拟上传失败")
	}
	c.files[path] = memoryFile{data: append([]byte(nil), data...), mode: mode}
	return nil
}

func (c *memoryClient) Rename(_ context.Context, source, target string) error {
	if c.renameError != "" && (strings.Contains(source, c.renameError) || strings.Contains(target, c.renameError)) {
		return errors.New("模拟重命名失败")
	}
	if c.restoreError && strings.Contains(source, ".scert-backup-") && !strings.Contains(target, ".scert-backup-") {
		return errors.New("模拟恢复备份失败")
	}
	value, ok := c.files[source]
	if !ok {
		return errors.New("源文件不存在")
	}
	delete(c.files, source)
	c.files[target] = value
	return nil
}

func (c *memoryClient) Remove(_ context.Context, path string) error {
	if c.removeError != "" && strings.Contains(path, c.removeError) {
		return errors.New("模拟删除失败")
	}
	delete(c.files, path)
	return nil
}

func (c *memoryClient) Run(_ context.Context, command []string) (CommandResult, error) {
	c.commands = append(c.commands, append([]string(nil), command...))
	index := c.runIndex
	c.runIndex++
	var result CommandResult
	var err error
	if index < len(c.runResults) {
		result = c.runResults[index]
	}
	if index < len(c.runErrors) {
		err = c.runErrors[index]
	}
	return result, err
}

func (c *memoryClient) Close() error { return nil }

type memoryFactory struct {
	clients map[string]*memoryClient
	errors  map[string]error
}

func (f memoryFactory) Connect(_ context.Context, server Server) (Client, error) {
	if err := f.errors[server.ID]; err != nil {
		return nil, err
	}
	return f.clients[server.ID], nil
}

func TestDeployReplacesFilesAndKeepsBackup(t *testing.T) {
	client := newMemoryClient()
	server := testServer("server-1")
	client.files[server.CertificatePath] = memoryFile{data: []byte("old-cert"), mode: 0o644}
	client.files[server.CertificateChainPath] = memoryFile{data: []byte("old-chain"), mode: 0o644}
	client.files[server.PrivateKeyPath] = memoryFile{data: []byte("old-key"), mode: 0o600}
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})
	service.now = func() time.Time { return time.Date(2026, 8, 27, 1, 2, 3, 4, time.UTC) }
	report := service.Deploy(context.Background(), []Target{{Server: server}}, testArtifact())
	if report.Status != "success" || report.Success != 1 {
		t.Fatalf("部署结果错误：%+v", report)
	}
	if string(client.files[server.CertificatePath].data) != "new-cert" || client.files[server.PrivateKeyPath].mode != 0o600 {
		t.Fatalf("远程证书内容或权限错误：%+v", client.files)
	}
	if len(client.commands) != 2 || len(client.commands[0]) != 2 || client.commands[0][1] != "-t" || len(client.commands[1]) != 3 || client.commands[1][0] != "nginx" {
		t.Fatalf("配置校验或 reload 命令错误：%v", client.commands)
	}
	backupCount := 0
	for path := range client.files {
		if strings.Contains(path, ".scert-backup-") {
			backupCount++
		}
	}
	if backupCount != 3 {
		t.Fatalf("成功部署后应保留三份远程备份：%d", backupCount)
	}
}

func TestDeployRollsBackWhenReloadFails(t *testing.T) {
	client := newMemoryClient()
	client.runResults = []CommandResult{{ExitCode: 0}, {ExitCode: 1}}
	server := testServer("server-1")
	client.files[server.CertificatePath] = memoryFile{data: []byte("old-cert"), mode: 0o644}
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})
	report := service.Deploy(context.Background(), []Target{{Server: server}}, testArtifact())
	if report.Status != "failed" || report.Failed != 1 {
		t.Fatalf("reload 失败结果错误：%+v", report)
	}
	if string(client.files[server.CertificatePath].data) != "old-cert" {
		t.Fatalf("reload 失败后旧证书未恢复：%+v", client.files)
	}
	if _, ok := client.files[server.PrivateKeyPath]; ok {
		t.Fatal("原本不存在的私钥文件应在回滚时删除")
	}
	for path := range client.files {
		if strings.Contains(path, ".scert-tmp-") || strings.Contains(path, ".scert-backup-") {
			t.Fatalf("回滚后不应残留临时文件或备份文件：%s", path)
		}
	}
}

func TestDeployRollsBackWhenValidationFailsWithoutReload(t *testing.T) {
	client := newMemoryClient()
	client.runResults = []CommandResult{{ExitCode: 1}}
	server := testServer("server-1")
	client.files[server.CertificatePath] = memoryFile{data: []byte("old-cert"), mode: 0o644}
	client.files[server.CertificateChainPath] = memoryFile{data: []byte("old-chain"), mode: 0o644}
	client.files[server.PrivateKeyPath] = memoryFile{data: []byte("old-key"), mode: 0o600}
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})
	report := service.Deploy(context.Background(), []Target{{Server: server}}, testArtifact())
	if report.Status != "failed" || report.Failed != 1 || !strings.Contains(report.Results[0].Message, "配置校验失败") {
		t.Fatalf("配置校验失败结果错误：%+v", report)
	}
	if len(client.commands) != 1 || client.commands[0][1] != "-t" {
		t.Fatalf("配置校验失败后不应执行 reload：%v", client.commands)
	}
	if string(client.files[server.CertificatePath].data) != "old-cert" || string(client.files[server.CertificateChainPath].data) != "old-chain" || string(client.files[server.PrivateKeyPath].data) != "old-key" {
		t.Fatalf("配置校验失败后旧证书未完整恢复：%+v", client.files)
	}
	for path := range client.files {
		if strings.Contains(path, ".scert-tmp-") || strings.Contains(path, ".scert-backup-") {
			t.Fatalf("配置校验回滚后不应残留临时文件或备份文件：%s", path)
		}
	}
}

func TestDeployReportsRollbackFailure(t *testing.T) {
	client := newMemoryClient()
	client.runResults = []CommandResult{{ExitCode: 0}, {ExitCode: 1}}
	client.restoreError = true
	server := testServer("server-1")
	client.files[server.CertificatePath] = memoryFile{data: []byte("old-cert"), mode: 0o644}
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})
	report := service.Deploy(context.Background(), []Target{{Server: server}}, testArtifact())
	if report.Status != "failed" || report.Failed != 1 {
		t.Fatalf("回滚失败结果错误：%+v", report)
	}
	if !strings.Contains(report.Results[0].Message, "自动回滚失败") || !strings.Contains(report.Results[0].Message, "恢复备份文件") {
		t.Fatalf("应报告自动回滚失败原因：%s", report.Results[0].Message)
	}
}

func TestDeployReportsPartialSuccess(t *testing.T) {
	first := newMemoryClient()
	second := newMemoryClient()
	second.uploadError = "fullchain.pem"
	firstServer := testServer("first")
	secondServer := testServer("second")
	service := NewService(memoryFactory{clients: map[string]*memoryClient{"first": first, "second": second}})
	report := service.Deploy(context.Background(), []Target{{Server: firstServer}, {Server: secondServer}}, testArtifact())
	if report.Status != "partial" || report.Success != 1 || report.Failed != 1 {
		t.Fatalf("部分成功汇总错误：%+v", report)
	}
}

func TestDeployRejectsDuplicateCertificatePaths(t *testing.T) {
	client := newMemoryClient()
	server := testServer("server-1")
	server.PrivateKeyPath = server.CertificatePath
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})

	report := service.Deploy(context.Background(), []Target{{Server: server}}, testArtifact())
	if report.Status != "failed" || report.Failed != 1 {
		t.Fatalf("重复远程路径应直接失败：%+v", report)
	}
	if !strings.Contains(report.Results[0].Message, "不能使用相同路径") {
		t.Fatalf("重复远程路径错误提示不正确：%s", report.Results[0].Message)
	}
	if len(client.files) != 0 {
		t.Fatalf("路径校验失败前不应连接并写入远程文件：%+v", client.files)
	}
}

func TestDeployRejectsRelativeCertificatePaths(t *testing.T) {
	client := newMemoryClient()
	server := testServer("server-1")
	server.CertificatePath = "cert.pem"
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})

	report := service.Deploy(context.Background(), []Target{{Server: server}}, testArtifact())
	if report.Status != "failed" || !strings.Contains(report.Results[0].Message, "必须是绝对路径") {
		t.Fatalf("相对远程路径应被拒绝：%+v", report)
	}
}

func testServer(id string) Server {
	return Server{
		ID: id, Name: id, Address: "192.0.2.10", Port: 22, Username: "deploy",
		PrivateKeyRef: "secret://ssh/" + id, HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/etc/scert/cert.pem", CertificateChainPath: "/etc/scert/fullchain.pem",
		PrivateKeyPath: "/etc/scert/key.pem", WebServer: WebServerNginx,
	}
}

func testArtifact() Artifact {
	return Artifact{Certificate: []byte("new-cert"), FullChain: []byte("new-chain"), PrivateKey: []byte("new-key")}
}
