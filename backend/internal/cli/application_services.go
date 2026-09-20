package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type applicationHTTP interface {
	ListenAndServe() error
	Listening() <-chan struct{}
}

type applicationControl interface {
	Serve(context.Context, localreset.Factory) error
	Close() error
}

// runApplicationServices 将本机通道绑定到正式主服务生命周期，并在数据库关闭前等待重置处理退出。
func runApplicationServices(
	ctx context.Context,
	web applicationHTTP,
	startRenewal func(context.Context),
	factory localreset.Factory,
	openControl func() (applicationControl, error),
) (_ bool, resultErr error) {
	if ctx.Err() != nil {
		return false, nil
	}
	control, err := openControl()
	if err != nil {
		return false, fmt.Errorf("启动本机密码重置通道失败：%w", err)
	}
	controlCtx, cancel := context.WithCancel(ctx)
	controlDone := make(chan error, 1)
	go func() { controlDone <- control.Serve(controlCtx, factory) }()
	controlFinished := false
	defer func() {
		cancel()
		resultErr = errors.Join(resultErr, control.Close())
		if !controlFinished {
			resultErr = errors.Join(resultErr, <-controlDone)
		}
	}()
	httpDone := make(chan error, 1)
	go func() { httpDone <- web.ListenAndServe() }()
	select {
	case <-ctx.Done():
		return false, nil
	case err := <-controlDone:
		controlFinished = true
		return false, applicationControlExit(ctx, err)
	case err := <-httpDone:
		return false, applicationHTTPExit(err)
	case <-web.Listening():
	}
	if ctx.Err() != nil {
		return false, nil
	}
	if startRenewal != nil {
		startRenewal(ctx)
	}
	select {
	case <-ctx.Done():
		return false, nil
	case err := <-controlDone:
		controlFinished = true
		return false, applicationControlExit(ctx, err)
	case err := <-httpDone:
		return false, applicationHTTPExit(err)
	}
}

func applicationControlExit(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	return fmt.Errorf("本机密码重置通道意外退出：%w", errors.Join(localreset.ErrUnavailable, err))
}

func applicationHTTPExit(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
