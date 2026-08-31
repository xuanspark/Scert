//go:build linux

package servicehost

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnitFileIncludesExecutableConfigurationAndWorkDir(t *testing.T) {
	content, err := renderUnitFile(Options{
		Name:        "Scert",
		Description: "Scert SSL 证书管理服务",
		Executable:  filepath.Join("/opt", "Scert", "scert"),
		ConfigPath:  filepath.Join("/opt", "Scert data", "scert.json"),
		WorkDir:     filepath.Join("/opt", "Scert data"),
	})
	if err != nil {
		t.Fatalf("渲染 systemd unit 失败：%v", err)
	}
	for _, expected := range []string{
		"[Unit]",
		"Description=Scert SSL 证书管理服务",
		"After=network-online.target",
		"[Service]",
		"Type=simple",
		"WorkingDirectory=\"/opt/Scert data\"",
		"ExecStart=\"/opt/Scert/scert\" --config \"/opt/Scert data/scert.json\" serve",
		"Restart=on-failure",
		"[Install]",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("systemd unit 缺少 %q：\n%s", expected, content)
		}
	}
}

func TestUnitFilePathRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../Scert", "a/b", `a\\b`} {
		if _, err := unitFilePath(name); err == nil {
			t.Errorf("服务名称 %q 未被拒绝", name)
		}
	}
}
