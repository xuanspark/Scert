//go:build linux

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func readSecretInput(input io.Reader, output io.Writer, prompt string) (value string, err error) {
	file, ok := input.(*os.File)
	if !ok || file == nil {
		return "", errors.New("敏感输入必须来自交互式终端，不接受管道或普通输入流")
	}

	fd := int(file.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return "", errors.New("敏感输入必须来自交互式终端，不接受管道或普通输入流")
	}

	withoutEcho := *original
	withoutEcho.Lflag &^= unix.ECHO | unix.ECHONL
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &withoutEcho); err != nil {
		return "", fmt.Errorf("关闭终端回显失败：%w", err)
	}
	defer func() {
		restoreErr := unix.IoctlSetTermios(fd, unix.TCSETS, original)
		if err == nil && restoreErr != nil {
			err = fmt.Errorf("恢复终端状态失败：%w", restoreErr)
		}
	}()

	if _, err := fmt.Fprint(output, prompt, ": "); err != nil {
		return "", fmt.Errorf("显示敏感输入提示失败：%w", err)
	}
	line, readErr := bufio.NewReader(file).ReadString('\n')
	_, _ = fmt.Fprintln(output)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("读取敏感输入失败：%w", readErr)
	}
	return parseSecretInput(line)
}
