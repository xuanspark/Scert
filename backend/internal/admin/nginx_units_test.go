package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNginxActivationDeploymentPolicy(t *testing.T) {
	for name, required := range map[string][]string{
		"scert-nginx.socket": {
			"ListenStream=/run/scert-nginx/activation.sock", "Accept=no", "SocketUser=root",
			"SocketGroup=Scert", "SocketMode=0660", "DirectoryMode=0755", "RemoveOnStop=no",
			"Service=scert-nginx.service", "WantedBy=sockets.target",
		},
		"scert-nginx.service": {
			"Requires=scert-nginx.socket nginx.service", "PartOf=nginx.service", "User=root", "Group=root",
			"ExecStart=/usr/local/libexec/scert-nginx --policy /etc/scert-nginx/policy.json",
			"NoNewPrivileges=yes", "ProtectSystem=strict", "ProtectHome=yes", "PrivateTmp=yes",
			"CapabilityBoundingSet=CAP_DAC_OVERRIDE CAP_CHOWN CAP_NET_BIND_SERVICE", "AmbientCapabilities=",
			"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "TimeoutStopSec=40",
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "systemd", name))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(activeUnitText(string(data)), "\n")
			for _, expected := range required {
				found := false
				for _, line := range lines {
					found = found || strings.TrimSpace(line) == expected
				}
				if !found {
					t.Errorf("辅助服务模板缺少安全策略：%s", expected)
				}
			}
			for _, line := range lines {
				if strings.HasPrefix(line, "ListenStream=") && line != "ListenStream=/run/scert-nginx/activation.sock" {
					t.Errorf("辅助服务不应监听其他地址：%s", line)
				}
				if strings.HasPrefix(line, "ExecStart=/scert/") || strings.HasPrefix(line, "ExecStartPre=") || strings.Contains(line, "--initialize") {
					t.Errorf("辅助服务不应执行应用目录程序或隐式初始化：%s", line)
				}
			}
		})
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "nginx", "policy.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		AllowedUID *uint32 `json:"allowed_uid"`
		SocketPath string  `json:"socket_path"`
		Directory  string  `json:"directory"`
		Hostname   string  `json:"hostname"`
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.AllowedUID != nil || policy.Hostname != "__MANAGEMENT_HOST__" || policy.SocketPath != "/run/scert-nginx/activation.sock" || policy.Directory != "/var/lib/scert-nginx" {
		t.Fatal("策略示例必须显式要求填写调用 UID 和域名，并匹配专用目录")
	}
}
