package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootExposesOnlyOperationalCommands(t *testing.T) {
	allowed := map[string]bool{
		"version": true, "init": true, "status": true, "serve": true,
		"certificate": true, "logs": true, "config": true,
		"backup": true, "restore": true, "service": true, "dns": true,
	}
	root := newRootCommand()
	for _, command := range root.Commands() {
		if command.Hidden {
			continue
		}
		if !allowed[command.Name()] {
			t.Errorf("正式 CLI 暴露了范围外命令：%s", command.Name())
		}
		delete(allowed, command.Name())
	}
	if len(allowed) != 0 {
		t.Fatalf("正式 CLI 缺少运维命令：%v", allowed)
	}
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"domain", "server", "ssh-key", "renew", "deploy"} {
		if strings.Contains(output.String(), "\n  "+name+" ") {
			t.Errorf("正式帮助仍展示历史业务命令：%s", name)
		}
	}
}

func TestRetiredBusinessCommandsRejectBeforeAccessingData(t *testing.T) {
	cases := [][]string{
		{"domain", "list"},
		{"domain", "add", "--name", "example.com"},
		{"domain", "issue", "--id", "example"},
		{"domain", "import", "--private-key", "not-a-real-key.pem"},
		{"server", "add", "--password", "test-secret-not-real"},
		{"server", "delete", "--id", "example"},
		{"ssh-key", "add", "--name", "example"},
		{"ssh-key", "delete", "--id", "example"},
		{"renew"},
		{"renew", "--id", "example"},
		{"deploy", "--id", "example"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[:min(2, len(args))], "_"), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "invalid.json")
			original := []byte("invalid-config-sentinel")
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			root := newRootCommand()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(append([]string{"--config", path, "--base-dir", directory}, args...))
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "此业务命令已停用，请使用 Web 管理页面") {
				t.Fatalf("旧入口未在读取无效配置前拒绝：%v", err)
			}
			if strings.Contains(output.String()+err.Error(), "test-secret-not-real") {
				t.Fatal("迁移提示泄露了旧命令中的凭据参数")
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(content, original) {
				t.Fatalf("被停用命令修改了配置：%v", readErr)
			}
			entries, readErr := os.ReadDir(directory)
			if readErr != nil || len(entries) != 1 {
				t.Fatalf("被停用命令创建了运行数据：%v", readErr)
			}
		})
	}
}
