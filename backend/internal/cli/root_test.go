package cli

import (
	"bytes"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/buildinfo"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestHelpIsLocalizedAndHasNoDuplicateCommands(t *testing.T) {
	configPath = ""
	baseDir = ""
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("执行帮助命令失败：%v", err)
	}
	text := output.String()
	for _, expected := range []string{"用法：", "可用命令：", "参数："} {
		if !strings.Contains(text, expected) {
			t.Fatalf("帮助缺少中文标题 %q：%s", expected, text)
		}
	}
	for _, unexpected := range []string{"Available Commands:", "Usage:", "Flags:", "completion"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("帮助仍包含未本地化或禁用内容 %q：%s", unexpected, text)
		}
	}
	if strings.Count(text, "\n  help ") != 1 {
		t.Fatalf("help 命令数量不正确：%s", text)
	}
}

func TestHelpSupportsSubcommand(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"help", "domain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("查看子命令帮助失败：%v", err)
	}
	text := output.String()
	if !strings.Contains(text, "管理托管域名") || !strings.Contains(text, "添加托管域名") {
		t.Fatalf("子命令帮助内容不正确：%s", text)
	}
}

func TestCommandStatusUsesApplicationVersion(t *testing.T) {
	cfg := config.Default(t.TempDir())
	status := commandStatus(cfg)
	if status["version"] != buildinfo.Version || status["config_version"] != config.CurrentVersion {
		t.Fatalf("状态版本字段不正确：%+v", status)
	}
}
