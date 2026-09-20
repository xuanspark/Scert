//go:build !linux && !windows

package adminconsole

import (
	"context"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"net"
)

func openTerminal(context.Context) (context.Context, terminal, func(), error) {
	return nil, nil, nil, localreset.ErrUnavailable
}
func dialControl(context.Context) (net.Conn, error) { return nil, localreset.ErrUnavailable }
