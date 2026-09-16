package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestTemporaryHTTPSDrainsCompletionResponse(t *testing.T) {
	material, err := NewTemporaryTLSMaterial("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	body := strings.Repeat("response-data", 10000)
	go func() {
		ok, err := serveTemporaryHTTPS(ctx, listener, material, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(completed)
			time.Sleep(30 * time.Millisecond)
			_, _ = io.WriteString(w, body)
		}), completed)
		if !ok && err == nil {
			err = context.Canceled
		}
		result <- err
	}()
	roots := x509.NewCertPool()
	roots.AddCert(material.Leaf)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("https://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != body {
		t.Fatalf("成功响应被关闭截断：%v，长度 %d", err, len(data))
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("临时服务没有关闭")
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("关闭后仍可连接")
	}
}

func TestBootstrapHandlerIsolatesBusinessRoutesAndSettings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "index.html"), []byte("bootstrap-page"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	cfg.Paths.Install = root
	sessions, token, err := NewSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(sessions, nil, nil, nil)
	handler, closeFiles := bootstrapHandler(router, cfg)
	defer closeFiles()
	for path, status := range map[string]int{
		"/": 200, "/api/v1/bootstrap/status": 200, "/api/v1/bootstrap/settings": 401,
		"/api/v1/setup": 404, "/api/v1/domains": 404, "/reset": 404, "/scert.json": 404,
		"/assets/../../config.json": 400,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != status {
			t.Fatalf("%s 状态码 %d，期望 %d", path, response.Code, status)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("临时页面允许缓存")
		}
	}
	session, err := sessions.Exchange(token, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/settings", nil)
	request.AddCookie(BootstrapSessionCookie(session.Token, session.ExpiresAt))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"mode":"bootstrap"`) {
		t.Fatal("认证后无法读取初始化设置")
	}
}
