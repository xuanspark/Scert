package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func TestNewWiresApplicationServices(t *testing.T) {
	cfg := readyApplicationConfig(t)
	application, err := NewWithConfigPath(context.Background(), cfg, filepath.Join(cfg.Paths.Config, "scert.json"))
	if err != nil {
		t.Fatalf("初始化应用失败：%v", err)
	}
	defer application.Close()

	if application.Repository == nil || application.Servers == nil || application.Tasks == nil {
		t.Fatal("数据库仓储未完整注入应用实例")
	}
	if application.Deployment == nil || application.Certificates == nil || application.Workflow == nil || application.Renewal == nil {
		t.Fatal("证书申请、部署或续期服务未完整注入应用实例")
	}
	if application.SecretStore == nil || application.SSHKeys == nil || application.DNSVerification == nil || application.HTTP == nil {
		t.Fatal("敏感信息、SSH、DNS 或 HTTP 服务未完整注入应用实例")
	}
}
func TestNewWithConfigPathRecoversInterruptedTasks(t *testing.T) {
	cfg := readyApplicationConfig(t)
	configPath := filepath.Join(cfg.Paths.Config, "scert.json")

	first, err := NewWithConfigPath(context.Background(), cfg, configPath)
	if err != nil {
		t.Fatalf("首次初始化应用失败：%v", err)
	}

	item, err := domain.NewDomain("restart.example.com", domain.CertificateSingle, nil)
	if err != nil {
		_ = first.Close()
		t.Fatalf("创建测试域名失败：%v", err)
	}
	if err := first.Repository.Create(context.Background(), item); err != nil {
		_ = first.Close()
		t.Fatalf("保存测试域名失败：%v", err)
	}

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	tasks := []repository.Task{
		{ID: "restart-queued", DomainID: item.ID, TaskType: "certificate_issue", Status: "queued", Stage: "queued", Message: "等待执行", CreatedAt: now, UpdatedAt: now},
		{ID: "restart-running", DomainID: item.ID, TaskType: "certificate_deploy", Status: "running", Stage: "deploy", Message: "正在部署", StartedAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: "restart-success", DomainID: item.ID, TaskType: "certificate_validate", Status: "success", Stage: "validate", Message: "已完成", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
	}
	for _, task := range tasks {
		if err := first.Tasks.CreateTask(context.Background(), task); err != nil {
			_ = first.Close()
			t.Fatalf("保存测试任务 %s 失败：%v", task.ID, err)
		}
	}
	attempt := repository.TaskAttempt{
		ID: "restart-attempt", TaskID: "restart-running", AttemptNumber: 1, Stage: "deploy", Status: "running", Message: "正在部署", StartedAt: &now, CreatedAt: now,
	}
	if err := first.Tasks.CreateAttempt(context.Background(), attempt); err != nil {
		_ = first.Close()
		t.Fatalf("保存测试任务尝试失败：%v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("关闭首次应用失败：%v", err)
	}

	second, err := NewWithConfigPath(context.Background(), cfg, configPath)
	if err != nil {
		t.Fatalf("重启后初始化应用失败：%v", err)
	}
	defer second.Close()

	for _, expectedID := range []string{"restart-queued", "restart-running"} {
		task, getErr := second.Tasks.GetTask(context.Background(), expectedID)
		if getErr != nil {
			t.Fatalf("读取恢复任务 %s 失败：%v", expectedID, getErr)
		}
		if task.Status != "failed" || task.Stage != "service_restart" || task.FinishedAt == nil {
			t.Fatalf("任务 %s 未按服务重启恢复：%+v", expectedID, task)
		}
		if task.Message != "服务重启导致任务中断，请重新执行" {
			t.Fatalf("任务 %s 中断说明不正确：%s", expectedID, task.Message)
		}
	}

	recoveredAttempt, err := second.Tasks.ListAttempts(context.Background(), "restart-running")
	if err != nil {
		t.Fatalf("读取恢复任务尝试失败：%v", err)
	}
	if len(recoveredAttempt) != 1 || recoveredAttempt[0].Status != "failed" || recoveredAttempt[0].FinishedAt == nil {
		t.Fatalf("任务尝试未按服务重启恢复：%+v", recoveredAttempt)
	}

	completed, err := second.Tasks.GetTask(context.Background(), "restart-success")
	if err != nil {
		t.Fatalf("读取已完成任务失败：%v", err)
	}
	if completed.Status != "success" || completed.Stage != "validate" || completed.Message != "已完成" {
		t.Fatalf("已完成任务不应被重启恢复逻辑修改：%+v", completed)
	}
}

func readyApplicationConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Paths = config.PathsConfig{
		Install: root, Config: filepath.Join(root, "conf"), Data: filepath.Join(root, "data"),
		Certificate: filepath.Join(root, "ssl"), Backup: filepath.Join(root, "backups"), Log: filepath.Join(root, "logs"),
	}
	cfg.Database.DSN = filepath.Join(cfg.Paths.Data, "scert.db")
	if err := setup.Apply(&cfg, setup.Input{
		AdminUsername: "admin", AdminEmail: "admin@example.test", AdminPassword: "Test1234!",
		ServerPort: 8080, WebHostname: "scert.example.test", WebEnabled: true,
		DNSProvider: "aliyun", DNSCredentialRef: "secret://dns/test", DNSVerified: true,
		CertificateRoot: cfg.Paths.Certificate, HTTPSMode: "direct",
	}); err != nil {
		t.Fatal(err)
	}
	cfg.SetupReady = true
	cfg.Web.InitReady = true
	return cfg
}

func TestUninitializedApplicationHasNoSideEffects(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Paths.Install = filepath.Join(root, "must-not-exist")
	if _, err := NewWithConfigPath(context.Background(), cfg, filepath.Join(root, "config.json")); err == nil {
		t.Fatal("未初始化配置未被拒绝")
	}
	if _, err := os.Stat(cfg.Paths.Install); !os.IsNotExist(err) {
		t.Fatalf("拒绝启动前产生了文件副作用：%v", err)
	}
}
