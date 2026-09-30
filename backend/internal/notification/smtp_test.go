package notification

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type smtpTestServer struct {
	listener     net.Listener
	tlsConfig    *tls.Config
	implicitTLS  bool
	failMailOnce bool
	rejectAuth   bool
	mu           sync.Mutex
	messages     [][]byte
	mailFrom     string
	connections  int
}

func newSMTPTestServer(t *testing.T, implicitTLS, startTLS bool) *smtpTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("create SMTP test listener: %v", err)
	}
	server := &smtpTestServer{listener: listener, implicitTLS: implicitTLS}
	if implicitTLS || startTLS {
		server.tlsConfig = newSMTPTestTLSConfig(t)
	}
	go server.serve(startTLS)
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *smtpTestServer) address() (string, int) {
	address := s.listener.Addr().(*net.TCPAddr)
	return address.IP.String(), address.Port
}

func (s *smtpTestServer) serve(startTLS bool) {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.connections++
		s.mu.Unlock()
		go s.handle(connection, startTLS)
	}
}

func (s *smtpTestServer) handle(connection net.Conn, startTLS bool) {
	defer connection.Close()
	if s.implicitTLS {
		tlsConnection := tls.Server(connection, s.tlsConfig)
		if err := tlsConnection.Handshake(); err != nil {
			return
		}
		connection = tlsConnection
	}
	reader := bufio.NewReader(connection)
	writeLine := func(value string) error {
		_, err := io.WriteString(connection, value+"\r\n")
		return err
	}
	if writeLine("220 smtp.test.local ESMTP") != nil {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if writeLine("250-smtp.test.local") != nil {
				return
			}
			if startTLS && writeLine("250-STARTTLS") != nil {
				return
			}
			if writeLine("250 AUTH PLAIN") != nil {
				return
			}
		case strings.HasPrefix(upper, "STARTTLS"):
			if !startTLS || writeLine("220 ready for TLS") != nil {
				return
			}
			tlsConnection := tls.Server(connection, s.tlsConfig)
			if err := tlsConnection.Handshake(); err != nil {
				return
			}
			connection = tlsConnection
			reader = bufio.NewReader(connection)
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			if s.rejectAuth {
				if writeLine("535 authentication failed") != nil {
					return
				}
				continue
			}
			if writeLine("235 authentication successful") != nil {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mu.Lock()
			s.mailFrom = command
			fail := s.failMailOnce
			s.failMailOnce = false
			s.mu.Unlock()
			if fail {
				if writeLine("451 temporary failure") != nil {
					return
				}
				return
			}
			if writeLine("250 sender accepted") != nil {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			if writeLine("250 recipient accepted") != nil {
				return
			}
		case upper == "DATA":
			if writeLine("354 end with <CRLF>.<CRLF>") != nil {
				return
			}
			var message strings.Builder
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				message.WriteString(dataLine)
			}
			s.mu.Lock()
			s.messages = append(s.messages, []byte(message.String()))
			s.mu.Unlock()
			if writeLine("250 queued") != nil {
				return
			}
		case upper == "QUIT":
			_ = writeLine("221 closing connection")
			return
		default:
			if writeLine("250 ok") != nil {
				return
			}
		}
	}
}

func (s *smtpTestServer) clientTLSConfig(host string) *tls.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	pool := x509.NewCertPool()
	if len(s.tlsConfig.Certificates) == 0 || len(s.tlsConfig.Certificates[0].Certificate) == 0 {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.tlsConfig.Certificates[0].Certificate[0]}))
	return &tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func (s *smtpTestServer) message() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return nil
	}
	return append([]byte(nil), s.messages[len(s.messages)-1]...)
}

func newSMTPTestTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate SMTP test key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatalf("generate SMTP test serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		DNSNames:     []string{"smtp.test.local"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("generate SMTP test certificate: %v", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	)
	if err != nil {
		t.Fatalf("load SMTP test certificate: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
}

func smtpTestConfig(host string, port int, mode string) config.NotificationConfig {
	return config.NotificationConfig{
		SMTPEnabled:       true,
		SMTPHost:          host,
		SMTPPort:          port,
		SMTPUsername:      "user",
		SMTPCredentialRef: "secret://notification/smtp/test",
		SMTPMode:          mode,
		TimeoutSecs:       2,
		RetryCount:        0,
		SenderName:        "Scert",
		SenderEmail:       "sender@example.com",
		Recipients:        []string{"receiver@example.com"},
	}
}

func TestSMTPRejectsPlainAuthenticationByDefault(t *testing.T) {
	server := newSMTPTestServer(t, false, false)
	host, port := server.address()
	service := New(smtpTestConfig(host, port, "plain"), credentialReaderStub{values: map[string]string{
		"username": "user",
		"password": "test-password",
	}})

	err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "\u660e\u6587\u8fde\u63a5\u7981\u6b62\u8ba4\u8bc1") {
		t.Fatalf("plain SMTP authentication should be rejected: %v", err)
	}
	if strings.Contains(err.Error(), "test-password") {
		t.Fatalf("SMTP errors must not expose password: %v", err)
	}
}

func TestSMTPSendsUTF8WithAllowedPlainAuthentication(t *testing.T) {
	server := newSMTPTestServer(t, false, false)
	host, port := server.address()
	cfg := smtpTestConfig(host, port, "plain")
	cfg.SMTPAllowPlaintextAuth = true
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"username": "user",
		"password": "test-password",
	}})

	if err := service.Send(context.Background(), Event{Type: "certificate_renewal", Status: "success", Domain: "example.test"}); err != nil {
		t.Fatalf("send UTF-8 SMTP notification: %v", err)
	}
	message := string(server.message())
	if !strings.Contains(message, "Subject: =?UTF-8?") || !strings.Contains(message, "example.test") {
		t.Fatalf("SMTP message was not generated correctly: %q", message)
	}
}

func TestSmailSystemSenderUsesSameEnvelopeAndHeader(t *testing.T) {
	for _, security := range []bool{false, true} {
		t.Run(map[bool]string{false: "notification", true: "security_code"}[security], func(t *testing.T) {
			server := newSMTPTestServer(t, false, true)
			host, port := server.address()
			cfg := smtpTestConfig(host, port, "starttls")
			cfg.SMTPUsername = "notify:scert@example.test"
			cfg.SenderEmail = "admin@other.test"
			service := New(cfg, credentialReaderStub{values: map[string]string{"password": "test-only-notification-credential"}})
			service.smtpTLSConfig = server.clientTLSConfig
			var err error
			if security {
				err = service.SendSecurityCode(t.Context(), "bound@example.test", "01234567")
			} else {
				err = service.Send(t.Context(), Event{Type: "certificate_issue", Status: "success", Domain: "example.test"})
			}
			if err != nil {
				t.Fatal(err)
			}
			server.mu.Lock()
			envelope := server.mailFrom
			server.mu.Unlock()
			if !strings.HasPrefix(envelope, "MAIL FROM:<notification+system@example.test>") {
				t.Fatal("SMTP 信封没有使用系统通知地址")
			}
			message := string(server.message())
			if !strings.Contains(message, formatSMTPAddress(address{Name: "系统通知", Email: "notification+system@example.test"})) || strings.Contains(message, "admin@other.test") {
				t.Fatal("邮件头没有使用固定系统通知身份，或泄漏旧管理员发件地址")
			}
			if security && strings.Contains(message, "receiver@example.com") {
				t.Fatal("验证码不得发送给普通通知收件人")
			}
		})
	}
}

func TestSmailSystemNotificationUsesSMTPOnly(t *testing.T) {
	server := newSMTPTestServer(t, true, false)
	host, port := server.address()
	calls := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer endpoint.Close()
	cfg := smtpTestConfig(host, port, "implicit_tls")
	cfg.SMTPUsername = "notify:scert@example.test"
	cfg.HTTPEnabled = true
	cfg.Endpoint = endpoint.URL
	cfg.HTTPPath = "/api/email/send"
	service := New(cfg, credentialReaderStub{values: map[string]string{"password": "test-only-notification-credential"}})
	service.smtpTLSConfig = server.clientTLSConfig
	if err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.test"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("Smail 系统通知不应调用 HTTP 通道，调用次数：%d", calls)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.messages) != 1 {
		t.Fatalf("SMTP 系统通知数量错误：%d", len(server.messages))
	}
}

func TestSMTPStartTLS(t *testing.T) {
	server := newSMTPTestServer(t, false, true)
	host, port := server.address()
	service := New(smtpTestConfig(host, port, "starttls"), credentialReaderStub{values: map[string]string{
		"username": "user",
		"password": "test-password",
	}})
	service.smtpTLSConfig = server.clientTLSConfig

	if err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("send SMTP STARTTLS notification: %v", err)
	}
}

func TestSMTPImplicitTLS(t *testing.T) {
	server := newSMTPTestServer(t, true, false)
	host, port := server.address()
	service := New(smtpTestConfig(host, port, "implicit_tls"), credentialReaderStub{values: map[string]string{
		"username": "user",
		"password": "test-password",
	}})
	service.smtpTLSConfig = server.clientTLSConfig

	if err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("send SMTP implicit TLS notification: %v", err)
	}
}

func TestSMTPRetriesTemporaryFailure(t *testing.T) {
	server := newSMTPTestServer(t, false, false)
	server.failMailOnce = true
	host, port := server.address()
	cfg := smtpTestConfig(host, port, "plain")
	cfg.SMTPAllowPlaintextAuth = true
	cfg.RetryCount = 1
	cfg.RetryIntervalsSecs = []int{0}
	service := New(cfg, credentialReaderStub{values: map[string]string{
		"username": "user",
		"password": "test-password",
	}})
	service.sleep = func(context.Context, time.Duration) error { return nil }

	if err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"}); err != nil {
		t.Fatalf("SMTP retry should recover from temporary failure: %v", err)
	}
	server.mu.Lock()
	connections := server.connections
	messages := len(server.messages)
	server.mu.Unlock()
	if connections < 2 || messages != 1 {
		t.Fatalf("SMTP retry result is incorrect: connections=%d messages=%d", connections, messages)
	}
}

func TestSMTPAuthFailureDoesNotLeakPassword(t *testing.T) {
	server := newSMTPTestServer(t, false, true)
	server.rejectAuth = true
	host, port := server.address()
	service := New(smtpTestConfig(host, port, "starttls"), credentialReaderStub{values: map[string]string{
		"username": "user",
		"password": "wrong-password",
	}})
	service.smtpTLSConfig = server.clientTLSConfig

	err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "\u8ba4\u8bc1\u5931\u8d25") {
		t.Fatalf("SMTP authentication failure should be reported: %v", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Fatalf("SMTP authentication errors must not expose password: %v", err)
	}
}

func TestSMTPRejectsMissingHost(t *testing.T) {
	cfg := smtpTestConfig("", 587, "starttls")
	service := New(cfg, nil)
	err := service.Send(context.Background(), Event{Type: "certificate_issue", Status: "success", Domain: "example.com"})
	if err == nil || !strings.Contains(err.Error(), "SMTP \u4e3b\u673a\u4e0d\u80fd\u4e3a\u7a7a") {
		t.Fatalf("missing SMTP host should return a clear error: %v", err)
	}
}

func TestSMTPAddressFormatting(t *testing.T) {
	value := formatSMTPAddress(address{Name: "\u7ba1\u7406\u670d\u52a1", Email: "admin@example.com"})
	if !strings.Contains(value, "admin@example.com") || !strings.Contains(value, "=?UTF-8?") {
		t.Fatalf("SMTP address encoding is incorrect: %s", value)
	}
}
