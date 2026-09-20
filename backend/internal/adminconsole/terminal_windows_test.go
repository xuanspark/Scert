//go:build windows

package adminconsole

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 这是项目原生平台回归测试，不替代提升权限管道和生产交互验收。
// 子进程使用隐藏的独立控制台，避免读取或修改开发者正在使用的终端。
func TestWindowsTerminalNative(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsTerminalNativeChild$", "-test.v", "-test.timeout=15s")
	command.Env = append(os.Environ(), "SCERT_TEST_NATIVE_CONSOLE=1")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("原生控制台回归失败：%v\n%s", err, output)
	}
	if !strings.Contains(string(output), "PASS: TestWindowsTerminalNativeChild") {
		t.Fatalf("原生控制台子测试没有运行：%s", output)
	}
}

func TestWindowsTerminalNativeChild(t *testing.T) {
	if os.Getenv("SCERT_TEST_NATIVE_CONSOLE") != "1" {
		t.Skip("仅由独立隐藏控制台中的子进程执行")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	handle := windows.Handle(input.Fd())
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		t.Fatal(err)
	}
	if err := readConsoleInput.Find(); err != nil {
		t.Fatalf("系统缺少所需的非阻塞控制台接口：%v", err)
	}
	flush := windows.NewLazySystemDLL("kernel32.dll").NewProc("FlushConsoleInputBuffer")
	write := windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	screen := &windowsTerminal{input: handle, output: os.Stdout}
	cases := []struct {
		name     string
		keys     string
		want     string
		hidden   bool
		cancel   bool
		deadline bool
		wantErr  error
	}{
		{name: "visible", keys: "1\r", want: "1"},
		{name: "secret", keys: "Native123\r", want: "Native123", hidden: true},
		{name: "backspace", keys: "abc\bD\r", want: "abD", hidden: true},
		{name: "surrogate", keys: "a\U0001F600\bb\r", want: "ab", hidden: true},
		{name: "escape", keys: "\x1b", hidden: true, wantErr: context.Canceled},
		{name: "interrupt", keys: "\x03", hidden: true, wantErr: context.Canceled},
		{name: "cancel", hidden: true, cancel: true, wantErr: context.Canceled},
		{name: "deadline", hidden: true, deadline: true, wantErr: context.DeadlineExceeded},
		{name: "overflow", keys: strings.Repeat("a", 4097), hidden: true, wantErr: ErrInvalidRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if ok, _, err := flush.Call(uintptr(handle)); ok == 0 {
				t.Fatal(err)
			}
			if err := windows.SetConsoleMode(handle, original|windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT); err != nil {
				t.Fatal(err)
			}
			var before uint32
			if err := windows.GetConsoleMode(handle, &before); err != nil {
				t.Fatal(err)
			}
			limit := 2 * time.Second
			if test.deadline {
				limit = 250 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			type result struct {
				value string
				err   error
			}
			done := make(chan result, 1)
			go func() {
				value, err := screen.Read(ctx, "原生控制台测试", test.hidden)
				done <- result{value, err}
			}()
			// 等待实际关闭回显后再输入，防止测试本身制造输入时序竞争。
			for {
				var active uint32
				if err := windows.GetConsoleMode(handle, &active); err != nil {
					t.Fatal(err)
				}
				if active&windows.ENABLE_ECHO_INPUT == 0 {
					break
				}
				select {
				case result := <-done:
					t.Fatalf("输入模式建立前读取已结束：%v", result.err)
				case <-ctx.Done():
					t.Fatal("未及时关闭控制台回显")
				case <-time.After(time.Millisecond):
				}
			}
			if test.cancel {
				cancel()
			} else if test.keys != "" {
				units := utf16.Encode([]rune(test.keys))
				records := make([]consoleRecord, len(units))
				for index, unit := range units {
					records[index] = consoleRecord{Type: 1, Down: 1, Repeat: 1, Character: unit}
				}
				var count uint32
				ok, _, err := write.Call(uintptr(handle), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&count)))
				if ok == 0 || count != uint32(len(records)) {
					t.Fatalf("写入原生控制台事件失败：%d %v", count, err)
				}
			}
			select {
			case result := <-done:
				if !errors.Is(result.err, test.wantErr) || result.value != test.want {
					t.Fatalf("读取结果不符：长度=%d，错误=%v", len(result.value), result.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("取消后控制台读取仍然阻塞")
			}
			var after uint32
			if err := windows.GetConsoleMode(handle, &after); err != nil || after != before {
				t.Fatalf("控制台模式未恢复：before=%x after=%x error=%v", before, after, err)
			}
		})
	}
}
