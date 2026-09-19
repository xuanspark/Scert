package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestHTTPSListeningSignalFollowsAcceptLoop(t *testing.T) {
	f := newRotationFixture(t)
	server := New(Dependencies{Config: f.server.config})
	host, err := server.prepareHTTPServer()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.Listening():
		t.Fatal("准备证书不代表端口已监听")
	default:
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- host.ServeTLS(&servingListener{Listener: listener, ready: server.httpListening}, "", "")
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("HTTPS 服务没有退出")
		}
	})
	select {
	case <-server.Listening():
	case err := <-done:
		t.Fatal("HTTPS 在监听前退出", err)
	case <-time.After(3 * time.Second):
		t.Fatal("HTTPS 接收循环未发布监听信号")
	}
	for range 2 {
		connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", listener.Addr().String(), &tls.Config{
			RootCAs: f.pool, ServerName: "management.example.test", MinVersion: tls.VersionTLS12,
		})
		if err != nil {
			t.Fatal("收到监听信号后无法建立严格 TLS 连接", err)
		}
		_ = connection.Close()
	}
}

func TestHTTPSBindFailureDoesNotSignalListening(t *testing.T) {
	f := newRotationFixture(t)
	_, port, err := net.SplitHostPort(f.address)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.server.config
	cfg.Server.Port, err = strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	server := New(Dependencies{Config: cfg})
	defer server.Shutdown(t.Context())
	if err := server.ListenAndServe(); err == nil {
		t.Fatal("被占用端口不能启动第二个监听器")
	}
	select {
	case <-server.Listening():
		t.Fatal("绑定失败不能报告已监听")
	default:
	}
}
