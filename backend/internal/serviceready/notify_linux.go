package serviceready

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Ready 仅通过服务管理器提供的本机套接字报告监听状态，不代表业务健康检查通过。
func Ready(status string) error {
	address := os.Getenv("NOTIFY_SOCKET")
	if address == "" {
		return nil
	}
	if len(address) < 2 || (address[0] != '/' && address[0] != '@') || strings.ContainsAny(address, "\r\n\x00") {
		return errors.New("服务就绪通知地址必须是本机套接字")
	}
	if strings.ContainsAny(status, "\r\n\x00") {
		return errors.New("服务就绪状态包含非法控制字符")
	}
	connection, err := net.DialTimeout("unixgram", address, time.Second)
	if err != nil {
		return fmt.Errorf("连接本机服务管理器失败：%w", err)
	}
	defer connection.Close()
	if err := connection.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return fmt.Errorf("设置服务就绪通知超时失败：%w", err)
	}
	message := "READY=1\nSTATUS=" + status
	written, err := connection.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("发送服务就绪通知失败：%w", err)
	}
	if written != len(message) {
		return errors.New("服务就绪通知发送不完整")
	}
	return nil
}
