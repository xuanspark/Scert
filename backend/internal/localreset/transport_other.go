//go:build !linux && !windows

package localreset

import (
	"context"
	"net"
)

const DefaultPath = ""

func Listen(string) (*Server, error)                         { return nil, ErrUnavailable }
func Dial(context.Context, string, uint32) (net.Conn, error) { return nil, ErrUnavailable }
