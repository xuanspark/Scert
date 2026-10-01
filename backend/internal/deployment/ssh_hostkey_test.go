package deployment

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestSSHFactoryConnectsUsingRegisteredHostKeyAcrossAlgorithms(t *testing.T) {
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestPasswordSSHServerWithHandler(t, func(password string) error {
		if password != "test-password" {
			return errors.New("unexpected test password")
		}
		return nil
	}, serveTestSFTP, ecdsaHostSigner, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/hostkey-fallback": {"password": "test-password"},
		},
		Timeout: 2 * time.Second,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoED25519,
		},
	}
	client, err := factory.Connect(t.Context(), testSSHServerModel(
		server,
		"secret://ssh/hostkey-fallback",
		ssh.FingerprintSHA256(ed25519HostSigner.PublicKey()),
	))
	if err != nil {
		t.Fatalf("factory should use the registered ED25519 host key after the ECDSA mismatch: %v", err)
	}
	defer client.Close()
	if server.accepted.Load() != 2 {
		t.Fatalf("expected two host-key algorithm attempts, got %d", server.accepted.Load())
	}
	if exists, err := client.Exists(t.Context(), "."); err != nil || !exists {
		t.Fatalf("SFTP connection is unavailable: exists=%t err=%v", exists, err)
	}
}

func TestSSHFactorySkipsUnavailableSafeHostKeyAlgorithm(t *testing.T) {
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestPasswordSSHServerWithHandler(t, func(password string) error {
		if password != "test-password" {
			return errors.New("unexpected test password")
		}
		return nil
	}, serveTestSFTP, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/hostkey-unavailable": {"password": "test-password"},
		},
		Timeout: 2 * time.Second,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoED25519,
		},
	}
	client, err := factory.Connect(t.Context(), testSSHServerModel(
		server,
		"secret://ssh/hostkey-unavailable",
		ssh.FingerprintSHA256(ed25519HostSigner.PublicKey()),
	))
	if err != nil {
		t.Fatalf("unavailable safe host-key algorithm should be skipped: %v", err)
	}
	client.Close()
	if server.accepted.Load() != 2 {
		t.Fatalf("expected two host-key algorithm attempts, got %d", server.accepted.Load())
	}
}

func TestSSHFactoryRejectsUnregisteredHostKeysAcrossAlgorithms(t *testing.T) {
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestPasswordSSHServerWithHandler(t, func(password string) error {
		if password != "test-password" {
			return errors.New("unexpected test password")
		}
		return nil
	}, serveTestSFTP, ecdsaHostSigner, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/hostkey-reject": {"password": "test-password"},
		},
		Timeout: 2 * time.Second,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoED25519,
		},
	}
	_, err := factory.Connect(t.Context(), testSSHServerModel(
		server,
		"secret://ssh/hostkey-reject",
		"SHA256:unregistered-test-host-key",
	))
	if err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("unregistered host key fingerprint must be rejected: %v", err)
	}
	if server.accepted.Load() != 2 {
		t.Fatalf("expected two host-key algorithm attempts, got %d", server.accepted.Load())
	}
}

func TestSSHFactoryStopsAfterMatchedHostKeyAuthenticationFailure(t *testing.T) {
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestPasswordSSHServerWithHandler(t, func(string) error {
		return errors.New("test authentication rejected")
	}, serveTestSFTP, ecdsaHostSigner, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/auth-reject": {"password": "test-password"},
		},
		Timeout: 2 * time.Second,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoED25519,
		},
	}
	_, err := factory.Connect(t.Context(), testSSHServerModel(
		server,
		"secret://ssh/auth-reject",
		ssh.FingerprintSHA256(ecdsaHostSigner.PublicKey()),
	))
	if err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("authentication failure after a matched host key must return directly: %v", err)
	}
	if server.accepted.Load() != 1 {
		t.Fatalf("authentication failure must not try another host-key algorithm, got %d attempts", server.accepted.Load())
	}
}
func TestSSHFactoryDefaultHostKeyAlgorithmsExcludeCertificates(t *testing.T) {
	algorithms, err := (SSHFactory{}).secureHostKeyAlgorithms()
	if err != nil {
		t.Fatalf("默认 SSH 主机密钥算法过滤失败：%v", err)
	}
	if len(algorithms) == 0 {
		t.Fatal("默认 SSH 主机密钥算法不能为空")
	}
	for _, algorithm := range algorithms {
		if strings.HasSuffix(algorithm, "-cert-v01@openssh.com") {
			t.Fatalf("默认算法不应包含 OpenSSH 证书算法：%s", algorithm)
		}
	}
}
func TestSSHFactoryFiltersUnsupportedAndDuplicateHostKeyAlgorithms(t *testing.T) {
	factory := SSHFactory{HostKeyAlgorithms: []string{
		"ssh-dss",
		ssh.KeyAlgoED25519,
		ssh.KeyAlgoED25519,
	}}
	algorithms, err := factory.secureHostKeyAlgorithms()
	if err != nil {
		t.Fatalf("安全算法过滤失败：%v", err)
	}
	if len(algorithms) != 1 || algorithms[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("算法过滤结果不正确：%v", algorithms)
	}
	factory.HostKeyAlgorithms = []string{"ssh-dss"}
	if _, err := factory.secureHostKeyAlgorithms(); err == nil {
		t.Fatal("仅包含不安全或不支持算法时必须拒绝")
	}
}

type testSSHServer struct {
	listener       net.Listener
	accepted       atomic.Int32
	host           string
	port           int
	config         *ssh.ServerConfig
	channelHandler func(ssh.Channel, <-chan *ssh.Request)
}

func startTestSSHServer(
	t *testing.T,
	authorize func(ssh.PublicKey) error,
	hostSigners ...ssh.Signer,
) *testSSHServer {
	return startTestSSHServerWithHandler(t, authorize, serveTestSFTP, hostSigners...)
}

func startTestSSHServerWithHandler(
	t *testing.T,
	authorize func(ssh.PublicKey) error,
	handler func(ssh.Channel, <-chan *ssh.Request),
	hostSigners ...ssh.Signer,
) *testSSHServer {
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, authorize(key)
		},
	}
	return startTestSSHServerWithConfig(t, config, handler, hostSigners...)
}

func startTestPasswordSSHServerWithHandler(
	t *testing.T,
	authorize func(string) error,
	handler func(ssh.Channel, <-chan *ssh.Request),
	hostSigners ...ssh.Signer,
) *testSSHServer {
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, authorize(string(password))
		},
	}
	return startTestSSHServerWithConfig(t, config, handler, hostSigners...)
}

func startTestSSHServerWithConfig(
	t *testing.T,
	config *ssh.ServerConfig,
	handler func(ssh.Channel, <-chan *ssh.Request),
	hostSigners ...ssh.Signer,
) *testSSHServer {
	t.Helper()
	for _, signer := range hostSigners {
		config.AddHostKey(signer)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test SSH server: %v", err)
	}
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatalf("failed to parse test SSH address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		listener.Close()
		t.Fatalf("failed to parse test SSH port: %v", err)
	}
	server := &testSSHServer{
		listener:       listener,
		host:           host,
		port:           port,
		config:         config,
		channelHandler: handler,
	}
	t.Cleanup(func() {
		listener.Close()
	})
	go server.serve()
	return server
}
func (s *testSSHServer) serve() {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.accepted.Add(1)
		go s.serveConnection(connection)
	}
}

func (s *testSSHServer) serveConnection(connection net.Conn) {
	defer connection.Close()
	_, channels, requests, err := ssh.NewServerConn(connection, s.config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			channelRequest.Reject(ssh.UnknownChannelType, "session channel required")
			continue
		}
		channel, requests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		go s.channelHandler(channel, requests)
	}
}

func serveTestSFTP(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			request.Reply(false, nil)
			continue
		}
		var subsystem struct {
			Name string
		}
		if err := ssh.Unmarshal(request.Payload, &subsystem); err != nil || subsystem.Name != "sftp" {
			request.Reply(false, nil)
			continue
		}
		if err := request.Reply(true, nil); err != nil {
			return
		}
		server, err := sftp.NewServer(channel)
		if err != nil {
			return
		}
		_ = server.Serve()
		_ = server.Close()
		return
	}
}

func newTestSSHSigner(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成测试 ED25519 密钥失败：%v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("创建测试 SSH 签名器失败：%v", err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "scert-ssh-test")
	if err != nil {
		t.Fatalf("编码测试 SSH 私钥失败：%v", err)
	}
	return signer, string(pem.EncodeToMemory(block))
}

func newTestECDSAHostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成测试 ECDSA 主机密钥失败：%v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("创建测试 ECDSA 主机签名器失败：%v", err)
	}
	return signer
}

func testSSHServerModel(server *testSSHServer, privateKeyRef, fingerprint string) Server {
	return Server{
		ID:                   fmt.Sprintf("server-%d", server.port),
		Name:                 "多主机密钥测试服务器",
		Address:              server.host,
		Port:                 server.port,
		Username:             "deploy",
		AuthMethod:           SSHAuthPassword,
		PasswordRef:          privateKeyRef,
		HostKeyFingerprint:   fingerprint,
		CertificatePath:      "/tmp/scert-test-cert.pem",
		PrivateKeyPath:       "/tmp/scert-test-key.pem",
		CertificateChainPath: "/tmp/scert-test-fullchain.pem",
		WebServer:            WebServerNginx,
	}
}
