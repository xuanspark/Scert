package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScertSystemdUnitPolicy(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	unitPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "deploy", "systemd", "Scert.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("读取 Scert systemd unit 失败：%v", err)
	}
	unit := activeUnitText(string(data))
	for _, required := range []string{
		"Documentation=file:///scert/docs/Scert-service-management.md",
		"User=Scert",
		"Group=Scert",
		"ExecStart=/scert/scert --config /scert/conf/scert.json serve",
		"Restart=on-failure",
		"UMask=0077",
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
		"ReadWritePaths=/ssl /scert/conf /scert/data /scert/log /scert/backups",
		"[Install]",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("Scert systemd unit 缺少必要策略：%s", required)
		}
	}
	for _, forbidden := range []string{"ExecReload=", "Restart=always"} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("Scert systemd unit 不得包含：%s", forbidden)
		}
	}
}
