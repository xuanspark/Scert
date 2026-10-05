//go:build !linux

package cli

import (
	"errors"
	"io"
)

func readSecretInput(io.Reader, io.Writer, string) (string, error) {
	return "", errors.New("敏感输入仅支持 Linux 交互式终端")
}
