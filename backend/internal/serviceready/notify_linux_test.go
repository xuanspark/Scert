package serviceready

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestReadySocket(t *testing.T) {
	for _, mode := range []string{"path", "abstract"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notify")
			if mode == "abstract" {
				path = "@" + path
			}
			socket, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
			if err != nil {
				t.Fatal(err)
			}
			defer socket.Close()
			t.Setenv("NOTIFY_SOCKET", path)
			status := "HTTPS 已监听，业务状态独立检查"
			if err := Ready(status); err != nil {
				t.Fatal(err)
			}
			if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 1024)
			n, _, err := socket.ReadFromUnix(buffer)
			if err != nil || string(buffer[:n]) != "READY=1\nSTATUS="+status {
				t.Fatalf("收到的通知不正确：%q，%v", buffer[:n], err)
			}
		})
	}
}

func TestReadyWithoutManager(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := Ready("已监听"); err != nil {
		t.Fatal(err)
	}
}

func TestReadyRejectsInvalidSocket(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8443", "relative", "/", "@", "/tmp/notify\nREADY=1"} {
		t.Setenv("NOTIFY_SOCKET", address)
		if err := Ready("已监听"); err == nil {
			t.Fatal("接受了非本机或非法通知地址")
		}
	}
}

func TestReadyRejectsStatusInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify")
	socket, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	for _, status := range []string{"状态\nREADY=1", "状态\rSTOPPING=1", "状态\x00"} {
		if err := Ready(status); err == nil {
			t.Fatal("接受了状态字段注入")
		}
	}
}

func TestReadyMissingSocketFails(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "missing"))
	if err := Ready("已监听"); err == nil {
		t.Fatal("不存在的通知套接字被当作成功")
	}
}
