package nginxactivation

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func identityChild(t *testing.T, mode string) {
	t.Helper()
	path := os.Getenv("SCERT_IDENTITY_TEST_SOCKET")
	if mode == "server" {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		defer listener.Close()
		_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Println("LISTENING")
		connection, err := listener.AcceptUnix()
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		var data [1]byte
		n, err := connection.Read(data[:])
		if n != 0 || err != io.EOF {
			t.Fatal("客户端向不可信服务发送了数据", n, err)
		}
		fmt.Println("IDENTITY_VERIFIED")
		return
	}
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal("未到达内核身份检查边界", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	err = writeFrame(connection, request{Version: protocolVersion, Action: "state"})
	var result response
	if err == nil {
		err = readFrame(connection, &result)
	}
	if mode == "allowed" {
		if err != nil || result.Code != "ok" || result.State == nil || result.State.Status != "idle" {
			t.Fatal("合法非特权进程未通过身份校验", err)
		}
	} else if err == nil {
		t.Fatal("非授权进程获得了响应")
	}
	fmt.Println("IDENTITY_VERIFIED")
}

func TestRootPeerIdentity(t *testing.T) {
	if mode := os.Getenv("SCERT_IDENTITY_TEST_CHILD"); mode != "" {
		identityChild(t, mode)
		return
	}
	if os.Getenv("SCERT_TEST_ROOT_IPC") != "1" {
		t.Skip("仅在显式启用的 root 隔离测试单元内执行不同 UID 测试")
	}
	if os.Geteuid() != 0 {
		t.Fatal("不同 UID 测试要求隔离单元以 root 运行")
	}
	base := t.TempDir()
	for _, path := range []string{base, filepath.Dir(base)} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 为降权子进程复制独立测试程序，避免放宽正式目录的访问权限。
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	executable := filepath.Join(base, "identity.test")
	copy, err := os.OpenFile(executable, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(copy, source)
	closeErr := copy.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	command := func(mode, path string, uid uint32) (*exec.Cmd, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRootPeerIdentity$")
		cmd.Env = append(os.Environ(), "SCERT_IDENTITY_TEST_CHILD="+mode, "SCERT_IDENTITY_TEST_SOCKET="+path)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		return cmd, cancel
	}
	t.Run("caller", func(t *testing.T) {
		f := newRotationFixture(t)
		path := filepath.Join(base, "service.sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		// 此用例故意放宽 inode 权限，只验证内核 UID 拒绝，而非文件权限拒绝。
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		service := LocalService{Rotation: f.rotation, AllowedUID: 65534}
		go func() { done <- service.Serve(ctx, listener) }()
		defer stopLocalService(t, cancel, done)
		for _, peer := range []struct {
			mode string
			uid  uint32
		}{{"allowed", 65534}, {"denied", 65533}} {
			cmd, stop := command(peer.mode, path, peer.uid)
			output, err := cmd.CombinedOutput()
			stop()
			if err != nil || !strings.Contains(string(output), "IDENTITY_VERIFIED") {
				t.Fatalf("UID %d 身份验证失败：%s, %v", peer.uid, output, err)
			}
		}
		if state, err := f.rotation.State(context.Background()); err != nil || state.Status != "idle" {
			t.Fatal("身份验证改变了事务状态", state, err)
		}
	})
	t.Run("server", func(t *testing.T) {
		parent := filepath.Join(base, "server")
		if err := os.Mkdir(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0o777); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "service.sock")
		cmd, cancel := command("server", path, 65534)
		defer cancel()
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cancel(); _ = cmd.Wait() }()
		scanner := bufio.NewScanner(output)
		if !scanner.Scan() || scanner.Text() != "LISTENING" {
			t.Fatal("身份模拟服务未启动")
		}
		// inode 看起来由 root 管理，但真正的监听创建者仍是非特权进程。
		if err := os.Chown(path, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := validateSocket(path, 0); err != nil {
			t.Fatal("未到达服务内核身份检查", err)
		}
		_, err = (Client{SocketPath: path}).State(context.Background())
		if err == nil || err.Error() != "本地激活服务身份不可信" {
			t.Fatal("未拒绝服务身份伪装", err)
		}
		verified := false
		for scanner.Scan() {
			verified = verified || scanner.Text() == "IDENTITY_VERIFIED"
		}
		if err := cmd.Wait(); err != nil || !verified {
			t.Fatal("身份模拟服务验证失败", err, stderr.String())
		}
	})
}
