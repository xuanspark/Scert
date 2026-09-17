package nginxactivation

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientDone := make(chan error, 1)
	go func() {
		_, err := (Client{SocketPath: path, ServiceUID: uint32(os.Geteuid())}).State(ctx)
		clientDone <- err
	}()
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	var message request
	if err := readFrame(connection, &message); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-clientDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("取消原因丢失", err)
		}
	case <-time.After(time.Second):
		t.Fatal("客户端取消后没有及时关闭连接")
	}
}

func TestServiceUnexpectedClose(t *testing.T) {
	f := newRotationFixture(t)
	path := filepath.Join(t.TempDir(), "a.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	service := LocalService{Rotation: f.rotation, AllowedUID: uint32(os.Geteuid())}
	if err := service.Serve(context.Background(), listener); !errors.Is(err, net.ErrClosed) {
		t.Fatal("监听器异常关闭不应报告正常退出", err)
	}
}
