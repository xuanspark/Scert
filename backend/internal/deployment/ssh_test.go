package deployment

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type staticSecretReader map[string]map[string]string

func (s staticSecretReader) Get(_ context.Context, reference string) (map[string]string, error) {
	return s[reference], nil
}

func TestSSHFactoryParsesEncryptedPrivateKeyBeforeDialing(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "scert-deployment-test", []byte("deployment-passphrase"))
	if err != nil {
		t.Fatalf("failed to encrypt test private key: %v", err)
	}
	privateKey := string(pem.EncodeToMemory(block))
	server := Server{
		ID: "server-test", Name: "test server", Address: "127.0.0.1", Port: 1,
		Username: "deploy", PrivateKeyRef: "secret://ssh/test", HostKeyFingerprint: "SHA256:test",
		CertificatePath: "/tmp/cert.pem", PrivateKeyPath: "/tmp/key.pem",
		CertificateChainPath: "/tmp/fullchain.pem", WebServer: WebServerNginx,
	}
	factory := SSHFactory{
		Secrets: staticSecretReader{server.PrivateKeyRef: {"private_key": privateKey, "passphrase": "deployment-passphrase"}},
		Timeout: 100 * time.Millisecond,
	}
	if _, err := factory.Connect(t.Context(), server); err == nil || !strings.Contains(err.Error(), "\u8fde\u63a5 SSH \u670d\u52a1\u5668\u5931\u8d25") {
		t.Fatalf("encrypted private key should parse before the expected dial failure: %v", err)
	}
	factory.Secrets = staticSecretReader{server.PrivateKeyRef: {"private_key": privateKey, "passphrase": "wrong-passphrase"}}
	if _, err := factory.Connect(t.Context(), server); err == nil || !strings.Contains(err.Error(), "\u89e3\u6790 SSH \u79c1\u94a5\u5931\u8d25") {
		t.Fatalf("wrong passphrase should be rejected before dialing: %v", err)
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

func TestJoinShellArgsQuotesArguments(t *testing.T) {
	command := []string{"nginx", "-c", "/etc/nginx/my config.conf", "reload'check"}
	joined := joinShellArgs(command)
	if !strings.Contains(joined, "'/etc/nginx/my config.conf'") || !strings.Contains(joined, "'reload'\\''check'") {
		t.Fatalf("远程命令参数未正确转义：%s", joined)
	}
}
