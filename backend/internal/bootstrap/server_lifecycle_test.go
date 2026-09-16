package bootstrap

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

func TestHTTPSBootstrapCompletesSQLiteAndStartsApplication(t *testing.T) {
	fixture := newCompletionFixture(t, "authorized.example.test")
	store, err := secretstore.Open(fixture.base.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	databases := NewDatabaseVerificationManager(fixture.base, store)
	dns := setup.NewDNSVerificationManager(completionDNSTester{}, store)
	sessions, token, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	completer := NewCompleter(CompleteDependencies{
		BaseConfig: fixture.base, ConfigPath: fixture.configPath,
		DatabaseSessions: databases, DNSSessions: dns, Credentials: store,
		Sessions: sessions, Firewall: fixture.firewall, Certificate: fixture.issuer,
	})
	router := NewRouter(sessions, databases, dns, completer)
	handler, closeFiles := bootstrapHandler(router, fixture.base)
	defer closeFiles()
	material, err := NewTemporaryTLSMaterial("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		ok, err := serveTemporaryHTTPS(ctx, listener, material, handler, router.Completed())
		if err == nil && !ok {
			err = errors.New("初始化未完成")
		}
		result <- err
	}()
	roots := x509.NewCertPool()
	roots.AddCert(material.Leaf)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 10 * time.Second}
	origin := "https://" + address
	post := func(path string, input any, output any) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, origin+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("初始化步骤 %s 失败：状态 %d，读取错误 %v", path, response.StatusCode, err)
		}
		if err := json.Unmarshal(data, output); err != nil {
			t.Fatal(err)
		}
	}
	var authenticated map[string]any
	post("/api/v1/bootstrap/session", map[string]string{"token": token}, &authenticated)
	var databaseResult, dnsResult struct {
		Token string `json:"verification_token"`
	}
	post("/api/v1/bootstrap/database/test", DatabaseInput{Driver: "sqlite"}, &databaseResult)
	post("/api/v1/bootstrap/dns/test", bootstrapDNSTestRequest{
		Provider: "aliyun", TestDomain: "authorized.example.test",
		Credentials: map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"},
	}, &dnsResult)
	input := fixture.request("")
	input.WebPublicIP = "203.0.113.20"
	input.ServerPort = listener.Addr().(*net.TCPAddr).Port
	input.DatabaseVerificationToken = databaseResult.Token
	input.DNSVerificationToken = dnsResult.Token
	var completed CompleteResult
	post("/api/v1/bootstrap/complete", input, &completed)
	if !completed.Completed || completed.ManagementURL != "https://"+net.JoinHostPort(input.WebPublicIP, strconv.Itoa(input.ServerPort))+"/" {
		t.Fatal("初始化未返回正确管理地址")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("初始化服务未退出")
	}
	parsed, _ := url.Parse(origin)
	if len(jar.Cookies(parsed)) != 0 {
		t.Fatal("完成响应未清除初始化会话")
	}
	// 清理未领取凭据不能删除已提交配置持有的凭据。
	databases.DiscardAllExcept()
	dns.DiscardAll()
	stored, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	stored.Server.Host = "127.0.0.1"
	application, err := app.NewWithConfigPath(ctx, stored, fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	applicationResult := make(chan error, 1)
	var closeOnce sync.Once
	closeApplication := func() {
		closeOnce.Do(func() {
			if err := application.Close(); err != nil {
				t.Error(err)
			}
			select {
			case err := <-applicationResult:
				if !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("正式服务关闭错误：%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("正式服务关闭超时")
			}
		})
	}
	defer closeApplication()
	go func() { applicationResult <- application.HTTP.ListenAndServe() }()
	// 正式 IP 证书与临时证书不同，只信任本次生成的正式证书。
	certificatePEM, err := os.ReadFile(stored.Server.TLSCert)
	if err != nil {
		t.Fatal(err)
	}
	formalRoots := x509.NewCertPool()
	if !formalRoots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("正式管理证书无法解析")
	}
	// 仅连接回环套接字，证书身份仍按配置中的文档示例公网 IP 完整校验。
	formalTransport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: formalRoots, ServerName: input.WebPublicIP,
	}}
	defer formalTransport.CloseIdleConnections()
	formalClient := &http.Client{Transport: formalTransport, Jar: jar, Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := formalClient.Get(origin + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal("正式服务健康检查失败")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("正式 HTTPS 未就绪：%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for path, expected := range map[string]int{
		"/api/v1/bootstrap/status":   http.StatusNotFound,
		"/api/v1/bootstrap/settings": http.StatusNotFound,
		"/api/v1/auth/session":       http.StatusUnauthorized,
	} {
		response, err := formalClient.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("切换后 %s 状态错误：%d", path, response.StatusCode)
		}
	}
	loginBody, err := json.Marshal(map[string]string{"identifier": input.AdminUsername, "password": input.AdminPassword})
	if err != nil {
		t.Fatal(err)
	}
	login, err := http.NewRequest(http.MethodPost, origin+"/api/v1/auth/login", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", origin)
	response, err := formalClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("新管理员无法登录正式 HTTPS：%d", response.StatusCode)
	}
	for _, path := range []string{"/api/v1/auth/session", "/api/v1/users", "/readyz"} {
		response, err := formalClient.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("初始化后会话、管理员权限或本地就绪检查失败：%s %d", path, response.StatusCode)
		}
	}
	closeApplication()
}

func TestBootstrapRunCancellationRemovesURLWithoutCreatingBusinessDatabase(t *testing.T) {
	fixture := newCompletionFixture(t, "authorized.example.test")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture.base.Server.Host = "127.0.0.1"
	fixture.base.Server.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		ok, err := Run(ctx, ServerOptions{
			Config: fixture.base, ConfigPath: fixture.configPath,
			PublicIP: "127.0.0.1", URLPath: fixture.urlPath, Output: &output,
		})
		if ok {
			err = errors.New("取消操作误报初始化完成")
		}
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	var address []byte
	for {
		address, err = os.ReadFile(fixture.urlPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("初始化地址文件没有生成")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后初始化未退出")
	}
	parsed, err := url.Parse(strings.TrimSpace(string(address)))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Fragment == "" || strings.Contains(output.String(), parsed.Fragment) {
		t.Fatal("一次性令牌缺失或泄露到日志")
	}
	if !strings.Contains(output.String(), "Bootstrap 临时证书 SHA-256 指纹：") {
		t.Fatal("初始化启动日志缺少可通过可信运维通道核对的证书指纹")
	}
	for _, path := range []string{fixture.urlPath, fixture.configPath, filepath.Join(fixture.base.Paths.Data, "scert.db")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("取消后不应保留初始化地址或提前产生正式文件：%s", path)
		}
	}
}
