//go:build !linux

package certstorage

import (
	"context"
	"errors"
)

func Prepare(context.Context, string) error {
	return errors.New("当前平台需要运行账户具有所选目录的创建权限")
}
