//go:build linux

package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("当前 Linux 环境无法打开伪终端：%v", err)
	}
	master = os.NewFile(uintptr(masterFD), "pty-master")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	ptyNumber, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptyNumber), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func TestReadSecretInputRejectsPipe(t *testing.T) {
	_, err := readSecretInput(strings.NewReader("not-a-secret\n"), io.Discard, "请输入密码")
	if err == nil || !strings.Contains(err.Error(), "交互式终端") {
		t.Fatalf("管道输入未被拒绝：%v", err)
	}
}

func TestReadSecretInputDisablesAndRestoresEcho(t *testing.T) {
	master, slave := openTestPTY(t)
	before, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	result := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := readSecretInput(slave, &output, "请输入密码")
		result <- struct {
			value string
			err   error
		}{value: value, err: err}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		current, ioctlErr := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
		if ioctlErr == nil && current.Lflag&(unix.ECHO|unix.ECHONL) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("读取敏感输入时未关闭终端回显")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := master.Write([]byte("  valid-secret  \n")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("伪终端读取失败：%v", got.err)
		}
		if got.value != "  valid-secret  " {
			t.Fatalf("合法首尾空格未保留：%q", got.value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("伪终端读取超时")
	}

	after, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if after.Lflag != before.Lflag {
		t.Fatalf("终端状态未恢复：before=%#x after=%#x", before.Lflag, after.Lflag)
	}
	if strings.Contains(output.String(), "valid-secret") {
		t.Fatal("敏感值出现在提示输出中")
	}
}
