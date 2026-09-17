package nginxactivation

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startLocalService(t *testing.T, rotation Rotation, allowed uint32) (Client, context.CancelFunc, <-chan error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "activation.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil { t.Fatal(err) }
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	service := LocalService{Rotation: rotation, AllowedUID: allowed}
	go func() { done <- service.Serve(ctx, listener) }()
	t.Cleanup(cancel)
	return Client{SocketPath: path, ServiceUID: uint32(os.Geteuid())}, cancel, done
}

func stopLocalService(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil { t.Fatal(err) }
	case <-time.After(5 * time.Second):
		t.Fatal("本地激活服务未及时退出")
	}
}

func TestLocalServiceRoundTrip(t *testing.T) {
	f := newRotationFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid()))
	defer stopLocalService(t, cancel, done)
	ctx := context.Background()
	if ready, err := client.Ready(ctx); err != nil || ready.Hostname != testHostname {
		t.Fatal("就绪查询失败", ready, err)
	}
	id, err := client.Stage(ctx, f.candidate.chain, f.candidate.key)
	if err != nil { t.Fatal(err) }
	state, err := client.State(ctx)
	if err != nil || state.ID != id || state.Status != "staged" {
		t.Fatalf("事务查询失败：%+v, %v", state, err)
	}
	f.upstream.active.Store(&f.candidate.pair)
	if err := client.Commit(ctx, id); err != nil { t.Fatal(err) }
	state, err = client.State(ctx)
	if err != nil || state.Status != "committed" { t.Fatal(state, err) }
	if state, err := client.Reconcile(ctx, id); err != nil || state.Status != "committed" {
		t.Fatal(state, err)
	}
	if err := client.Commit(ctx, id); !errors.Is(err, ErrLocalOperation) {
		t.Fatal("重复提交应要求查询已知终态", err)
	}
}

func TestLocalServiceRejectsUnauthorizedPeer(t *testing.T) {
	f := newRotationFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid())+1)
	defer stopLocalService(t, cancel, done)
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := client.Stage(ctx, f.candidate.chain, f.candidate.key); err == nil {
		t.Fatal("未授权 UID 不应调用激活器")
	}
	if state, err := f.rotation.State(context.Background()); err != nil || state.Status != "idle" {
		t.Fatal("未授权调用改变了事务", state, err)
	}
}

func TestLocalServiceRejectsUnsafeMessages(t *testing.T) {
	f := newRotationFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid()))
	defer stopLocalService(t, cancel, done)
	for _, message := range []request{
		{Version: 1, Action: "exec"},
		{Version: 2, Action: "state"},
		{Version: 1, Action: "commit", ID: "../../config"},
		{Version: 1, Action: "state", Key: []byte("secret")},
		{Version: 1, Action: "stage", Chain: []byte("pem")},
	} {
		connection, err := net.Dial("unix", client.SocketPath)
		if err != nil { t.Fatal(err) }
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		if err := writeFrame(connection, message); err != nil { t.Fatal(err) }
		var result response
		err = readFrame(connection, &result)
		_ = connection.Close()
		if err != nil || result.Code != "invalid_request" {
			t.Fatal("无效请求没有被拒绝", result, err)
		}
	}
	connection, err := net.Dial("unix", client.SocketPath)
	if err != nil { t.Fatal(err) }
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], maxFrameSize+1)
	if _, err := connection.Write(header[:]); err != nil { t.Fatal(err) }
	var result response
	if err := readFrame(connection, &result); err != nil || result.Code != "invalid_request" {
		t.Fatal("超限帧没有立即拒绝", result, err)
	}
	state, err := f.rotation.State(context.Background())
	if err != nil || state.Status != "idle" { t.Fatal(state, err) }
}

func TestLocalServiceLostStageResponseCanReconcile(t *testing.T) {
	f := newRotationFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid()))
	defer stopLocalService(t, cancel, done)
	connection, err := net.Dial("unix", client.SocketPath)
	if err != nil { t.Fatal(err) }
	if err := writeFrame(connection, request{Version: 1, Action: "stage", Chain: f.candidate.chain, Key: f.candidate.key}); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	var state State
	for {
		state, err = client.State(ctx)
		if err == nil && state.Status == "staged" { break }
		if ctx.Err() != nil { t.Fatal("丢失响应后未找到暂存事务", state, err) }
		time.Sleep(10*time.Millisecond)
	}
	state, err = client.Reconcile(ctx, state.ID)
	if err != nil || state.Status != "rolled_back" { t.Fatal(state, err) }
}

func TestLocalServiceShutdownClosesStalledClients(t *testing.T) {
	f := newRotationFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid()))
	connection, err := net.Dial("unix", client.SocketPath)
	if err != nil { t.Fatal(err) }
	defer connection.Close()
	if _, err := connection.Write([]byte{0}); err != nil { t.Fatal(err) }
	stopLocalService(t, cancel, done)
	if _, err := os.Lstat(client.SocketPath); err != nil {
		t.Fatal("服务停止不应删除托管 socket", err)
	}
}

func TestLocalClientRejectsUntrustedSocket(t *testing.T) {
	f := newRotationFixture(t)
	client, cancel, done := startLocalService(t, f.rotation, uint32(os.Geteuid()))
	defer stopLocalService(t, cancel, done)
	wrong := client
	wrong.ServiceUID++
	if _, err := wrong.State(context.Background()); err == nil { t.Fatal("不应信任错误所有者") }
	link := filepath.Join(filepath.Dir(client.SocketPath), "link.sock")
	if err := os.Symlink(client.SocketPath, link); err != nil { t.Fatal(err) }
	wrong = client
	wrong.SocketPath = link
	if _, err := wrong.State(context.Background()); err == nil { t.Fatal("不应连接符号链接") }
	if err := os.Chmod(client.SocketPath, 0o666); err != nil { t.Fatal(err) }
	if _, err := client.State(context.Background()); err == nil { t.Fatal("不应连接公开可写 socket") }
}
