//go:build linux

package servicehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnitFileIncludesExecutableConfigurationAndWorkDir(t *testing.T) {
	content, err := renderUnitFile(Options{
		Name:           "Scert",
		Description:    "Scert SSL 证书管理服务",
		Executable:     filepath.Join("/opt", "Scert", "scert"),
		ConfigPath:     filepath.Join("/opt", "Scert data", "scert.json"),
		WorkDir:        filepath.Join("/opt", "Scert data"),
		User:           "Scert",
		Group:          "Scert",
		ReadWritePaths: []string{"/var/lib/scert", "/var/log/scert"},
	})
	if err != nil {
		t.Fatalf("渲染 systemd unit 失败：%v", err)
	}
	for _, expected := range []string{
		"[Unit]",
		"Description=Scert SSL 证书管理服务",
		"After=network-online.target",
		"RuntimeDirectory=scert",
		"RuntimeDirectoryMode=0700",
		"Environment=SCERT_BOOTSTRAP_FIREWALL_MODE=external",
		"Environment=SCERT_SERVER_HOST=127.0.0.1",
		"[Service]",
		"Type=simple",
		"User=Scert",
		"Group=Scert",
		"WorkingDirectory=\"/opt/Scert data\"",
		"ExecStart=\"/opt/Scert/scert\" --config \"/opt/Scert data/scert.json\" serve",
		"Restart=on-failure",
		"CapabilityBoundingSet=",
		"AmbientCapabilities=",
		"PrivateDevices=yes",
		"ProtectSystem=strict",
		"ProtectHostname=yes",
		"ProtectClock=yes",
		"ProtectKernelTunables=yes",
		"ProtectKernelModules=yes",
		"ProtectControlGroups=yes",
		"ProtectKernelLogs=yes",
		"LockPersonality=yes",
		"MemoryDenyWriteExecute=yes",
		"RestrictNamespaces=yes",
		"RestrictRealtime=yes",
		"RestrictSUIDSGID=yes",
		"RemoveIPC=yes",
		"SystemCallArchitectures=native",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
		"ReadWritePaths=\"/var/lib/scert\" \"/var/log/scert\"",
		"[Install]",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("systemd unit 缺少 %q：\n%s", expected, content)
		}
	}
}

func TestPackagedUnitAllowsInterfaceDiscoveryWithoutCapabilities(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "systemd", "Scert.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
		"CapabilityBoundingSet=",
		"AmbientCapabilities=",
		"NoNewPrivileges=yes",
		"Environment=SCERT_SERVER_HOST=127.0.0.1",
		"User=Scert",
	} {
		found := false
		for _, line := range strings.Split(string(content), "\n") {
			if strings.TrimSpace(line) == expected {
				found = true
			}
		}
		if !found {
			t.Errorf("发布服务缺少安全配置：%s", expected)
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

func TestRenderUnitFileRejectsRelativeWritablePath(t *testing.T) {
	_, err := renderUnitFile(Options{
		Name: "Scert", Executable: "/opt/scert", ConfigPath: "/opt/scert/scert.json", WorkDir: "/opt/scert",
		ReadWritePaths: []string{"relative/path"},
	})
	if err == nil {
		t.Fatal("相对可写路径未被拒绝")
	}
}
