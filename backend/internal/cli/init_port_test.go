package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func TestInitCommandDefaultsToPort80(t *testing.T) {
	command := newInitCommandWithFirewall(&cliFakeFirewallManager{})
	port, err := command.Flags().GetInt("server-port")
	if err != nil || port != 80 || command.Flag("server-port").DefValue != "80" {
		t.Fatalf("正式服务默认端口应为 80：port=%d err=%v", port, err)
	}
	if port := config.Default(t.TempDir()).Server.Port; port != 8080 {
		t.Fatalf("临时初始化监听端口不应被修改：%d", port)
	}
}

func TestInteractiveInitPreservesPortChoice(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		flag   string
		answer string
		want   int
	}{
		{name: "default", want: 80},
		{name: "custom_flag", flag: "8443", want: 8443},
		{name: "explicit_answer", flag: "8443", answer: "9443", want: 9443},
		{name: "minimum", flag: "1", want: 1},
		{name: "maximum", flag: "65535", want: 65535},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			command := newInitCommandWithFirewall(&cliFakeFirewallManager{})
			if scenario.flag != "" {
				if err := command.Flags().Set("server-port", scenario.flag); err != nil {
					t.Fatal(err)
				}
			}
			port, err := command.Flags().GetInt("server-port")
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Default(t.TempDir())
			cfg.Server.Port = 18080
			value := setup.Input{ServerPort: port}
			var output bytes.Buffer
			answers := []string{"admin", "admin@example.com", "password123", scenario.answer,
				"n", "n", "", "1", "test-only-token", "example.com", "", "panel.example.com", "y"}
			if err := readInteractiveInit(strings.NewReader(strings.Join(answers, "\n")+"\n"), &output, &value, cfg); err != nil {
				t.Fatalf("交互初始化失败：%v", err)
			}
			if value.ServerPort != scenario.want {
				t.Fatalf("交互端口应为 %d，实际为 %d", scenario.want, value.ServerPort)
			}
			if !strings.Contains(output.String(), fmt.Sprintf("Scert 主服务端口 * [%d]", port)) {
				t.Fatal("交互端口提示未使用正式服务参数默认值")
			}
			value.DNSCredentialRef = "secret://dns/cloudflare/test"
			value.DNSVerified = true
			if err := setup.Apply(&cfg, value); err != nil {
				t.Fatalf("应用初始化配置失败：%v", err)
			}
			if cfg.Server.Port != scenario.want {
				t.Fatalf("配置未保留正式服务端口：%d", cfg.Server.Port)
			}
		})
	}
}

func TestInitRejectsInvalidPortWithoutFallback(t *testing.T) {
	for _, port := range []int{-1, 0, 65536} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			cfg := config.Default(t.TempDir())
			value := setup.Input{ServerPort: port}
			answers := "admin\nadmin@example.com\npassword123\n\nn\nn\n\n1\ntest-only-token\nexample.com\n\npanel.example.com\ny\n"
			var output bytes.Buffer
			if err := readInteractiveInit(strings.NewReader(answers), &output, &value, cfg); err != nil {
				t.Fatal(err)
			}
			if value.ServerPort != port {
				t.Fatalf("非法端口不能静默回退：输入=%d 实际=%d", port, value.ServerPort)
			}
			if err := setup.Apply(&cfg, value); err == nil || !strings.Contains(err.Error(), "主服务端口无效") {
				t.Fatalf("应拒绝非法端口 %d：%v", port, err)
			}
		})
	}
}
