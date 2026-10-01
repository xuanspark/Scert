package deployment

import (
	"context"
	"strings"
	"testing"
	"time"
)

type staticSecretReader map[string]map[string]string

func (s staticSecretReader) Get(_ context.Context, reference string) (map[string]string, error) {
	return s[reference], nil
}

func TestSSHFactoryRejectsPrivateKeyAuthentication(t *testing.T) {
	server := Server{
		ID: "server-test", Name: "test server", Address: "127.0.0.1", Port: 1,
		Username: "deploy", PrivateKeyRef: "secret://ssh/test", HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/tmp/cert.pem", PrivateKeyPath: "/tmp/key.pem",
		CertificateChainPath: "/tmp/fullchain.pem", WebServer: WebServerNginx,
	}
	factory := SSHFactory{
		Secrets: staticSecretReader{server.PrivateKeyRef: {"private_key": "must-not-be-read"}},
		Timeout: 100 * time.Millisecond,
	}
	if _, err := factory.Connect(t.Context(), server); err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("private key authentication should be rejected before reading secrets: %v", err)
	}
}

func TestValidateCommandRejectsShellControlLines(t *testing.T) {
	for _, command := range [][]string{
		{"nginx", "-s", "reload\nrm -rf /"},
		{"nginx", "", "reload"},
		{"nginx", "-s", "reload\x00"},
	} {
		if err := validateCommand(command); err == nil {
			t.Fatalf("不安全远程命令未被拒绝：%q", command)
		}
	}
}

func TestValidateCommandAllowsShellScriptArgument(t *testing.T) {
	command := []string{"sh", "-c", "set -eu\nprintf '%s\\n' 'ok'"}
	if err := validateCommand(command); err != nil {
		t.Fatalf("shell script argument should be accepted: %v", err)
	}
}

func TestJoinShellArgsQuotesArguments(t *testing.T) {
	command := []string{"nginx", "-c", "/etc/nginx/my config.conf", "reload'check"}
	joined := joinShellArgs(command)
	if !strings.Contains(joined, "'/etc/nginx/my config.conf'") || !strings.Contains(joined, "'reload'\\''check'") {
		t.Fatalf("远程命令参数未正确转义：%s", joined)
	}
}

func TestAuthorizedKeyInstallScriptUsesRealNewline(t *testing.T) {
	script := authorizedKeyInstallScript("encoded-public-key")
	if !strings.Contains(script, "printf '%s' 'encoded-public-key'") {
		t.Fatalf("authorized key payload was not inserted safely: %s", script)
	}
	if !strings.Contains(script, "printf '%s\\n'") {
		t.Fatalf("authorized key should be terminated by a real newline: %s", script)
	}
	if strings.Contains(script, "printf '%s\\\\n'") {
		t.Fatalf("authorized key script still writes a literal backslash-n: %s", script)
	}
}
func TestAuthorizedKeyInstallScriptEnablesRootPublicKeyAuthentication(t *testing.T) {
	script := authorizedKeyInstallScript("encoded-public-key")
	for _, required := range []string{
		"PubkeyAuthentication yes",
		"AuthorizedKeysFile .ssh/authorized_keys",
		"sshd -t",
		"systemctl reload ssh",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("authorized key script is missing %q: %s", required, script)
		}
	}
}
