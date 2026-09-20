//go:build windows

package adminconsole

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"
	"unicode/utf16"
	"unsafe"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"golang.org/x/sys/windows"
)

var readConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputExW")

type consoleRecord struct {
	Type      uint16
	Padding   uint16
	Down      int32
	Repeat    uint16
	Key       uint16
	Scan      uint16
	Character uint16
	Control   uint32
}

type windowsTerminal struct {
	input  windows.Handle
	output *os.File
}

func openTerminal(ctx context.Context) (context.Context, terminal, func(), error) {
	if err := localreset.AuthorizeCurrentProcess(); err != nil {
		return nil, nil, nil, err
	}
	input := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(input, &mode) != nil || windows.GetConsoleMode(windows.Handle(os.Stderr.Fd()), &mode) != nil {
		return nil, nil, nil, ErrUnauthorizedPeer
	}
	if readConsoleInput.Find() != nil {
		return nil, nil, nil, localreset.ErrUnavailable
	}
	child, cancel := context.WithCancel(ctx)
	return child, &windowsTerminal{input: input, output: os.Stderr}, cancel, nil
}

func dialControl(ctx context.Context) (net.Conn, error) { return localreset.Dial(ctx, "", 0) }
func (t *windowsTerminal) Show(message string) error    { return showLine(t.output, message) }

// Read 使用非阻塞控制台事件，取消或超时后恢复原始模式，不遗留后台输入线程。
func (t *windowsTerminal) Read(ctx context.Context, prompt string, hidden bool) (value string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var mode uint32
	if err := windows.GetConsoleMode(t.input, &mode); err != nil {
		return "", err
	}
	next := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_QUICK_EDIT_MODE)
	next |= windows.ENABLE_EXTENDED_FLAGS
	if err := windows.SetConsoleMode(t.input, next); err != nil {
		return "", err
	}
	defer func() {
		if err := windows.SetConsoleMode(t.input, mode); err != nil {
			value, resultErr = "", localreset.ErrUnavailable
		}
	}()
	if _, err := fmt.Fprint(t.output, prompt+"： "); err != nil {
		return "", err
	}
	defer fmt.Fprintln(t.output)
	buffer := make([]uint16, 0, 4096)
	defer func() { clear(buffer[:cap(buffer)]) }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var record consoleRecord
		var count uint32
		ok, _, err := readConsoleInput.Call(uintptr(t.input), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count)), 2)
		if ok == 0 {
			return "", err
		}
		if count == 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if record.Type != 1 || record.Down == 0 {
			continue
		}
		switch record.Character {
		case 3, 27:
			return "", context.Canceled
		case '\r', '\n':
			return string(utf16.Decode(buffer)), nil
		case '\b':
			if len(buffer) > 0 {
				size := len(buffer) - 1
				if size > 0 && utf16.IsSurrogate(rune(buffer[size])) && utf16.IsSurrogate(rune(buffer[size-1])) {
					size--
				}
				clear(buffer[size:])
				buffer = buffer[:size]
				if !hidden {
					_, _ = fmt.Fprint(t.output, "\b \b")
				}
			}
		default:
			if record.Character < 32 || record.Character == 127 {
				continue
			}
			repeat := int(record.Repeat)
			if repeat < 1 {
				repeat = 1
			}
			if len(buffer)+repeat > cap(buffer) {
				return "", ErrInvalidRequest
			}
			for range repeat {
				buffer = append(buffer, record.Character)
			}
			if !hidden {
				_, _ = fmt.Fprint(t.output, string(utf16.Decode([]uint16{record.Character})))
			}
		}
	}
}
