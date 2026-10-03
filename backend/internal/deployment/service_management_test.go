package deployment

import (
	"context"
	"strings"
	"testing"
)

func TestDeployRejectsManagementDomainFromNginxConfigWhenPolicyFileIsMissing(t *testing.T) {
	client := newMemoryClient()
	server := testServer("nginx-management-server")
	client.runResults = []CommandResult{
		{ExitCode: 1},
		{ExitCode: 0, Output: []byte(`
http {
	server {
		listen 443 ssl;
		server_name odacm.cn;
		location / {
			proxy_pass https://127.0.0.1:8443;
			proxy_ssl_name odacm.cn;
			proxy_ssl_verify on;
		}
	}
}
`)},
	}
	service := NewService(memoryFactory{clients: map[string]*memoryClient{server.ID: client}})

	report := service.Deploy(context.Background(), []Target{{Server: server, DomainName: "odacm.cn"}}, testArtifact(t))
	if report.Status != "failed" || report.Failed != 1 {
		t.Fatalf("missing policy must reject management domain deployment: %+v", report)
	}
	if report.Results[0].FailureCode != FailureManagementConflict {
		t.Fatalf("unexpected failure code for missing policy: %+v", report.Results[0])
	}
	if len(client.commands) != 2 || client.commands[0][0] != "test" || client.commands[1][0] != "nginx" {
		t.Fatalf("unexpected missing-policy probe commands: %v", client.commands)
	}
}

func TestNginxServerBlockIsManagedRequiresAllProtectionDirectives(t *testing.T) {
	base := `server {
	server_name odacm.cn;
	proxy_pass https://127.0.0.1:8443;
	proxy_ssl_name odacm.cn;
	proxy_ssl_verify on;
}`
	cases := map[string]struct {
		config string
		want   bool
	}{
		"complete": {config: base, want: true},
		"missing_verify": {
			config: strings.Replace(base, "proxy_ssl_verify on;", "", 1),
		},
		"wrong_upstream": {
			config: strings.Replace(base, "https://127.0.0.1:8443", "https://127.0.0.1:9443", 1),
		},
		"wrong_sni": {
			config: strings.Replace(base, "proxy_ssl_name odacm.cn", "proxy_ssl_name other.example", 1),
		},
		"wrong_server_name": {
			config: strings.Replace(base, "server_name odacm.cn", "server_name other.example", 1),
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := nginxContainsManagedServer(testCase.config, "odacm.cn"); got != testCase.want {
				t.Fatalf("managed server detection mismatch: got=%t want=%t", got, testCase.want)
			}
		})
	}
}
