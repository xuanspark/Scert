package config

import (
	"errors"
	"net"
	"runtime"
)

func defaultServerHost() string {
	if runtime.GOOS == "linux" {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

// ValidateServerListener 校验环境覆盖后的实际监听配置，不通过域名解析判断回环地址。
func (c Config) ValidateServerListener() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	address := net.ParseIP(c.Server.Host)
	if address == nil || !address.IsLoopback() {
		return errors.New("Linux 管理服务只允许监听回环 IP 地址，请使用 127.0.0.1 或 ::1，并通过 Nginx 的 80/443 端口访问")
	}
	return nil
}
