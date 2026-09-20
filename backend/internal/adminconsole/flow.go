package adminconsole

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"git.pcux.cn/xuans/Scert/backend/internal/passwordpolicy"
)

var (
	ErrUnauthorizedPeer = localreset.ErrUnauthorized
	ErrInvalidRequest   = localreset.ErrProtocol
)

type terminal interface {
	Read(context.Context, string, bool) (string, error)
	Show(string) error
}

type resetClient interface {
	Methods() []localreset.Method
	Select(string) error
	Verify(string) error
	Reset(string) error
	Close() error
}

// Run 只接受无参数的本机交互，不从参数、环境变量、管道或文件读取密码。
func Run(parent context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("Scert-admin 不接受参数；Linux 请在管理员终端执行 systemctl start Scert-admin")
	}
	ctx, cancel := context.WithTimeout(parent, localreset.SessionTimeout)
	defer cancel()
	ctx, screen, closeScreen, err := openTerminal(ctx)
	if err != nil {
		return errors.New("本机管理员终端校验失败；Linux 请执行 systemctl start Scert-admin，Windows 请使用提升权限的控制台")
	}
	defer closeScreen()
	conn, err := dialControl(ctx)
	if err != nil {
		_ = screen.Show("本机重置通道不可用，请确认 Scert 主服务已完成初始化并正常运行。")
		return localreset.ErrUnavailable
	}
	return runConnected(ctx, conn, screen)
}

func runConnected(ctx context.Context, conn net.Conn, screen terminal) error {
	client, err := localreset.Begin(ctx, conn)
	if err != nil {
		_ = screen.Show("主服务拒绝了本机重置会话，请检查初始化状态及绑定邮箱。")
		return localreset.ErrUnavailable
	}
	defer client.Close()
	if err := resetFlow(ctx, client, screen); err != nil {
		_ = screen.Show("密码重置未完成：操作已取消、验证失败、会话过期或保存失败。请检查服务日志后重新启动。")
		return localreset.ErrProtocol
	}
	if err := screen.Show("超级管理员密码重置成功，旧登录会话已失效，请使用新密码登录。"); err != nil {
		return errors.New("密码已重置，但无法向调用终端显示结果，请使用新密码登录")
	}
	return nil
}

func resetFlow(ctx context.Context, client resetClient, screen terminal) error {
	methods := client.Methods()
	if len(methods) != 1 || methods[0].ID != "email" {
		return localreset.ErrUnavailable
	}
	choice, err := screen.Read(ctx, "验证方式：绑定邮箱验证码。输入 1 发送验证码，其他输入取消", false)
	if err != nil || strings.TrimSpace(choice) != "1" {
		return localreset.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := client.Select("email"); err != nil {
		return err
	}
	code, err := screen.Read(ctx, "请输入绑定邮箱收到的八位验证码（不回显）", true)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = client.Verify(code)
	code = ""
	if err != nil {
		return err
	}
	first, err := screen.Read(ctx, "请输入新密码（8 到 12 个字符，包含字母和数字，不回显）", true)
	if err != nil {
		return err
	}
	if err := passwordpolicy.Validate(first, ""); err != nil {
		return err
	}
	second, err := screen.Read(ctx, "请再次输入新密码（不回显）", true)
	if err != nil {
		first = ""
		return err
	}
	if first != second {
		first, second = "", ""
		return localreset.ErrProtocol
	}
	second = ""
	if err := ctx.Err(); err != nil {
		first = ""
		return err
	}
	err = client.Reset(first)
	first = ""
	return err
}

func clearBytes(value []byte) { clear(value) }

func stripPasswordLineEnding(value []byte) []byte {
	if len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
	}
	if len(value) > 0 && value[len(value)-1] == '\r' {
		value = value[:len(value)-1]
	}
	return value
}

func showLine(writer io.Writer, message string) error {
	_, err := fmt.Fprintln(writer, message)
	return err
}
