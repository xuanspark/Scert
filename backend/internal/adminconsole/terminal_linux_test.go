//go:build linux

package adminconsole

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testTerminal(t *testing.T) (*os.File, *invocationTerminal, context.Context) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "test-terminal")
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slaveFD, err := unix.Open(fmt.Sprintf("/dev/pts/%d", number), unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	slave := os.NewFile(uintptr(slaveFD), "test-terminal-input")
	t.Cleanup(func() { slave.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	terminal := &invocationTerminal{file: slave, cancel: cancel}
	return master, terminal, context.WithValue(ctx, invocationTerminalKey{}, terminal)
}

func readTerminalUntil(t *testing.T, master *os.File, suffix string) string {
	t.Helper()
	if err := master.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var result strings.Builder
	buffer := make([]byte, 1024)
	for !strings.Contains(result.String(), suffix) {
		n, err := master.Read(buffer)
		if err != nil {
			t.Fatalf("终端输出等待失败：%q %v", result.String(), err)
		}
		result.Write(buffer[:n])
	}
	return result.String()
}

func TestBoundTerminalInputAndPasswordEcho(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprintf("hidden=%v", hidden), func(t *testing.T) {
			master, terminal, ctx := testTerminal(t)
			done := make(chan error, 1)
			go func() {
				value, err := terminal.read(ctx, "第一行\n选项", hidden)
				if err == nil && value != "Test1234" {
					err = fmt.Errorf("输入未完整传递")
				}
				done <- err
			}()
			readTerminalUntil(t, master, "选项： ")
			if _, err := master.Write([]byte("Test1234\n")); err != nil {
				t.Fatal(err)
			}
			output := readTerminalUntil(t, master, "\r\n")
			if hidden && strings.Contains(output, "Test1234") {
				t.Fatal("密码出现回显")
			}
			if !hidden && !strings.Contains(output, "Test1234") {
				t.Fatal("可见输入未显示")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundTerminalCancellationRestoresTerminal(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(fmt.Sprintf("eof=%v", eof), func(t *testing.T) {
			master, terminal, parent := testTerminal(t)
			before, err := unix.IoctlGetTermios(int(terminal.file.Fd()), unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := terminal.read(ctx, "等待取消", true)
				done <- err
			}()
			readTerminalUntil(t, master, "等待取消： ")
			if eof {
				// 无前台进程组的测试终端用 EOF 模拟用户中止，真实 Ctrl+C 由调用者退出触发取消。
				_, _ = master.Write([]byte{4})
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("取消结果：%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("取消后仍然等待输入")
			}
			after, err := unix.IoctlGetTermios(int(terminal.file.Fd()), unix.TCGETS)
			if err != nil || *before != *after {
				t.Fatal("终端设置未恢复")
			}
		})
	}
}
