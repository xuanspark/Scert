package bootstrap

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunNotify(t *testing.T) {
	for _, mode := range []string{"listening", "missing-socket"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCompletionFixture(t, "authorized.example.test")
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			fixture.base.Server.Host = "127.0.0.1"
			fixture.base.Server.Port = listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			path := filepath.Join(t.TempDir(), "notify")
			t.Setenv("NOTIFY_SOCKET", path)
			var socket *net.UnixConn
			if mode == "listening" {
				socket, err = net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				complete, err := Run(ctx, ServerOptions{
					Config: fixture.base, ConfigPath: fixture.configPath,
					PublicIP: "127.0.0.1", URLPath: fixture.urlPath, FirewallMode: "external",
				})
				if complete {
					t.Error("启动通知被当作初始化完成")
				}
				result <- err
			}()
			if socket != nil {
				if err := socket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 1024)
				n, _, err := socket.ReadFromUnix(buffer)
				if err != nil || !strings.HasPrefix(string(buffer[:n]), "READY=1\nSTATUS=") || !strings.Contains(string(buffer[:n]), "业务尚未就绪") {
					t.Fatalf("实际初始化入口未报告等待配置状态：%v", err)
				}
				cancel()
			}
			select {
			case err := <-result:
				if mode == "missing-socket" && err == nil {
					t.Fatal("通知地址失效时初始化入口静默降级")
				}
				if mode == "listening" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("初始化入口在通知失败或取消后没有退出")
			}
		})
	}
}
