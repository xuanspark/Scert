package firewall

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	ModeAuto        = "auto"
	ModeExternal    = "external"
	BackendExternal = "external"
)

// NewManagerWithMode 仅接受本机启动配置指定的策略，不根据权限错误自动降级。
func NewManagerWithMode(mode string) (Manager, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeAuto:
		return NewManager(), nil
	case ModeExternal:
		return externalManager{}, nil
	default:
		return nil, errors.New("初始化防火墙模式只能是 auto 或 external")
	}
}

type externalManager struct{}

func (externalManager) OpenTCP(ctx context.Context, port int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := validatePort(port); err != nil {
		return Result{}, err
	}
	return Result{
		Backend: BackendExternal, Port: port,
		Message: fmt.Sprintf("防火墙由管理员管理，Scert 未检查或修改主机规则及云安全组。请确认正式管理 TCP 端口 %d 已按需放行；使用反向代理时仅允许代理访问内部端口。", port),
	}, nil
}

func (externalManager) Rollback(_ context.Context, result Result) error {
	if result.Created {
		return errors.New("外部管理模式不能回滚其他管理器创建的防火墙规则")
	}
	return nil
}
