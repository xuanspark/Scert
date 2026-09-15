package deployment

import (
	"bytes"
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
	clientSigner, privateKey := newTestSSHSigner(t)
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestSSHServer(t, func(key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return errors.New("测试客户端公钥不匹配")
		}
		return nil
	}, ecdsaHostSigner, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/hostkey-fallback": {"private_key": privateKey},
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
		t.Fatalf("应在 ECDSA 指纹不匹配后使用 ED25519 主机密钥连接：%v", err)
	}
	defer client.Close()
	if server.accepted.Load() != 2 {
		t.Fatalf("应分别尝试两个主机密钥算法，实际连接次数：%d", server.accepted.Load())
	}
	if exists, err := client.Exists(t.Context(), "."); err != nil || !exists {
		t.Fatalf("SFTP 连接不可用：exists=%t err=%v", exists, err)
	}
}

func TestSSHFactorySkipsUnavailableSafeHostKeyAlgorithm(t *testing.T) {
	clientSigner, privateKey := newTestSSHSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestSSHServer(t, func(key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return errors.New("测试客户端公钥不匹配")
		}
		return nil
	}, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/hostkey-unavailable": {"private_key": privateKey},
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
		t.Fatalf("不可用的安全主机密钥算法应被跳过：%v", err)
	}
	client.Close()
	if server.accepted.Load() != 2 {
		t.Fatalf("应在算法协商失败后尝试下一种安全算法，实际连接次数：%d", server.accepted.Load())
	}
}
func TestSSHFactoryRejectsUnregisteredHostKeysAcrossAlgorithms(t *testing.T) {
	clientSigner, privateKey := newTestSSHSigner(t)
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestSSHServer(t, func(key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
			return errors.New("测试客户端公钥不匹配")
		}
		return nil
	}, ecdsaHostSigner, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/hostkey-reject": {"private_key": privateKey},
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
	if err == nil || !strings.Contains(err.Error(), "SSH 主机指纹不匹配") {
		t.Fatalf("未登记的主机指纹必须被拒绝：%v", err)
	}
	if !strings.Contains(err.Error(), ssh.KeyAlgoECDSA256) || !strings.Contains(err.Error(), ssh.KeyAlgoED25519) {
		t.Fatalf("错误应包含已经安全校验的主机密钥算法：%v", err)
	}
	if server.accepted.Load() != 2 {
		t.Fatalf("错误指纹应校验两个可用算法，实际连接次数：%d", server.accepted.Load())
	}
}

func TestSSHFactoryStopsAfterMatchedHostKeyAuthenticationFailure(t *testing.T) {
	_, privateKey := newTestSSHSigner(t)
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	server := startTestSSHServer(t, func(ssh.PublicKey) error {
		return errors.New("测试认证拒绝")
	}, ecdsaHostSigner, ed25519HostSigner)

	factory := SSHFactory{
		Secrets: staticSecretReader{
			"secret://ssh/auth-reject": {"private_key": privateKey},
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
	if err == nil || !strings.Contains(err.Error(), "SSH 握手失败") {
		t.Fatalf("主机指纹匹配后的认证失败必须直接返回：%v", err)
	}
	if strings.Contains(err.Error(), "SSH 主机指纹不匹配") {
		t.Fatalf("认证失败不得继续尝试其他主机密钥算法：%v", err)
	}
	if server.accepted.Load() != 1 {
		t.Fatalf("认证失败后不得继续重连，实际连接次数：%d", server.accepted.Load())
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
	t.Helper()
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, authorize(key)
		},
	}
	for _, signer := range hostSigners {
		config.AddHostKey(signer)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动测试 SSH 服务失败：%v", err)
	}
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatalf("解析测试 SSH 地址失败：%v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		listener.Close()
		t.Fatalf("解析测试 SSH 端口失败：%v", err)
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
		PrivateKeyRef:        privateKeyRef,
		HostKeyFingerprint:   fingerprint,
		CertificatePath:      "/tmp/scert-test-cert.pem",
		PrivateKeyPath:       "/tmp/scert-test-key.pem",
		CertificateChainPath: "/tmp/scert-test-fullchain.pem",
		WebServer:            WebServerNginx,
	}
}
