//go:build linux

package adminconsole

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/sys/unix"
)

// 只在已授权 start 的终端输入，不向系统共享密码代理发布请求。
func (t *invocationTerminal) read(ctx context.Context, prompt string, hidden bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fd := int(t.file.Fd())
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return "", err
	}
	// 保留终端的行编辑、信号及换行模式，避免 systemctl 提前退出时影响父 shell。
	// 只有密码输入临时关闭回显，其余输入由终端正常显示。
	if hidden {
		protected := *state
		protected.Lflag &^= unix.ECHO | unix.ECHONL
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, &protected); err != nil {
			return "", err
		}
		defer unix.IoctlSetTermios(fd, unix.TCSETS, state)
		defer fmt.Fprintln(t.file)
	}
	prompt = strings.TrimRight(prompt, ":： ") + "： "
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	if _, err := fmt.Fprint(t.file, prompt); err != nil {
		return "", err
	}
	stream := &terminalStream{ctx: ctx, terminal: t}
	buffer := make([]byte, 4096)
	defer clearBytes(buffer)
	for size := 0; size < len(buffer); {
		n, err := stream.Read(buffer[size : size+1])
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.cancel()
				return "", context.Canceled
			}
			return "", err
		}
		size += n
		if size > 0 && buffer[size-1] == '\n' {
			return string(stripPasswordLineEnding(buffer[:size])), nil
		}
	}
	return "", ErrInvalidRequest
}

type terminalStream struct {
	ctx      context.Context
	terminal *invocationTerminal
}

func (s *terminalStream) Read(buffer []byte) (int, error) {
	fd := int(s.terminal.file.Fd())
	for {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			return 0, io.EOF
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, err := unix.Read(fd, buffer)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if n == 0 && err == nil {
			err = io.EOF
		}
		return n, err
	}
}
