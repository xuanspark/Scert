package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/serviceready"
)

func TestBootstrapListeningIsNotBusinessReady(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "listening"
		if fail {
			name = "notify-failure"
		}
		t.Run(name, func(t *testing.T) {
			material, err := NewTemporaryTLSMaterial("127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			cfg := config.Default(t.TempDir())
			handler, closeFiles := bootstrapHandler(nil, cfg)
			defer closeFiles()
			expected := errors.New("本机通知失败")
			notified := make(chan struct{})
			wrapped := &serviceready.Listener{Listener: listener, Notify: func() error {
				close(notified)
				if fail {
					return expected
				}
				return nil
			}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				complete, err := serveTemporaryHTTPS(ctx, wrapped, material, handler, make(chan struct{}))
				if complete {
					t.Error("监听成功被当作初始化完成")
				}
				result <- err
			}()
			select {
			case <-notified:
			case <-time.After(3 * time.Second):
				t.Fatal("初始化接收循环未发出通知")
			}
			if !fail {
				roots := x509.NewCertPool()
				roots.AddCert(material.Leaf)
				transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
				response, err := client.Get("https://" + listener.Addr().String() + "/readyz")
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "setup_incomplete") {
					t.Fatal("监听通知改变了初始化业务就绪边界")
				}
				cancel()
			}
			select {
			case err := <-result:
				if fail && !errors.Is(err, expected) {
					t.Fatalf("未保留通知失败原因：%v", err)
				}
				if !fail && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("初始化服务未按生命周期退出")
			}
		})
	}
}
