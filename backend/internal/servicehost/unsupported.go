//go:build !windows && !linux

package servicehost

import (
	"context"
	"errors"
)

var ErrUnsupported = errors.New("当前操作系统不支持系统服务管理")

func Install(_ context.Context, _ Options) error  { return ErrUnsupported }
func Uninstall(_ context.Context, _ string) error { return ErrUnsupported }
func Start(_ context.Context, _ string) error     { return ErrUnsupported }
func Stop(_ context.Context, _ string) error      { return ErrUnsupported }
