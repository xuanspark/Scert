package cli

import (
	"errors"
	"fmt"
	"strings"
)

const maxSecretInputBytes = 4096

func parseSecretInput(line string) (string, error) {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", errors.New("SMTP 密码不能为空")
	}
	if len([]byte(line)) > maxSecretInputBytes {
		return "", fmt.Errorf("SMTP 密码长度不能超过 %d 字节", maxSecretInputBytes)
	}
	return line, nil
}
