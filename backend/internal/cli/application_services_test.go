package cli

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type applicationTestHTTP struct {
	ready   chan struct{}
	exit    chan error
	started atomic.Int32
}

func (h *applicationTestHTTP) Listening() <-chan struct{} { return h.ready }
func (h *applicationTestHTTP) ListenAndServe() error {
	h.started.Add(1)
	return <-h.exit
}

type applicationTestControl struct {
	serve  func(context.Context, localreset.Factory) error
	closed atomic.Int32
}

func (c *applicationTestControl) Serve(ctx context.Context, factory localreset.Factory) error {
	return c.serve(ctx, factory)
}
func (c *applicationTestControl) Close() error {
	c.closed.Add(1)
	return nil
}

func applicationTestFactory(context.Context, localreset.Peer) (localreset.Session, error) {
	return nil, localreset.ErrUnavailable
}

func TestApplicationControlFailurePreventsHTTPStartup(t *testing.T) {
	web := &applicationTestHTTP{ready: make(chan struct{}), exit: make(chan error, 1)}
	expected := errors.New("test-control-bind-failure")
	_, err := runApplicationServices(t.Context(), web, nil, applicationTestFactory, func() (applicationControl, error) {
		return nil, expected
	})
	if !errors.Is(err, expected) || web.started.Load() != 0 {
		t.Fatal("本机通道创建失败后仍启动了网络服务")
	}
}

func TestApplicationCancellationBeforeStartupDoesNotOpenControl(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	opened := false
	_, err := runApplicationServices(ctx, nil, nil, nil, func() (applicationControl, error) {
		opened = true
		return nil, nil
	})
	if err != nil || opened {
		t.Fatal("取消后仍然启动本机通道")
	}
}

func TestApplicationBootstrapCannotOpenControl(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.SetupReady = false
	if _, err := runApplication(t.Context(), cfg, ""); !errors.Is(err, localreset.ErrUnavailable) {
		t.Fatal("初始化阶段没有拒绝正式本机通道")
	}
}

func TestApplicationWaitsForControlWorkBeforeReturning(t *testing.T) {
	for _, scenario := range []string{"cancel-starting", "cancel-running", "http-startup-error", "http-runtime-error", "http-closed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			web := &applicationTestHTTP{ready: make(chan struct{}), exit: make(chan error, 1)}
			t.Cleanup(func() { web.exit <- http.ErrServerClosed })
			entered := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			control := &applicationTestControl{serve: func(ctx context.Context, factory localreset.Factory) error {
				if factory == nil {
					t.Error("主服务未提供重置工厂")
				}
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-release
				close(finished)
				return nil
			}}
			renewal := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := runApplicationServices(ctx, web, func(context.Context) { close(renewal) }, applicationTestFactory, func() (applicationControl, error) { return control, nil })
				done <- err
			}()
			<-entered
			if scenario == "cancel-running" || scenario == "http-runtime-error" || scenario == "http-closed" {
				close(web.ready)
				<-renewal
			}
			expected := errors.New("test-http-failure")
			switch scenario {
			case "cancel-starting", "cancel-running":
				cancel()
			case "http-closed":
				web.exit <- http.ErrServerClosed
			default:
				web.exit <- expected
			}
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("没有取消本机重置处理")
			}
			select {
			case <-done:
				t.Fatal("业务处理尚未结束就返回，可能提前关闭数据库")
			default:
			}
			close(release)
			select {
			case err := <-done:
				if scenario == "http-startup-error" || scenario == "http-runtime-error" {
					if !errors.Is(err, expected) {
						t.Fatal("丢失 HTTP 启动或运行错误")
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("本机通道退出后主服务仍未返回")
			}
			<-finished
			if control.closed.Load() != 1 {
				t.Fatal("没有关闭本机通道")
			}
		})
	}
}

func TestApplicationControlUnexpectedExitFailsClosed(t *testing.T) {
	for _, ready := range []bool{false, true} {
		name := "starting"
		if ready {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			web := &applicationTestHTTP{ready: make(chan struct{}), exit: make(chan error, 1)}
			t.Cleanup(func() { web.exit <- http.ErrServerClosed })
			stop := make(chan struct{})
			control := &applicationTestControl{serve: func(context.Context, localreset.Factory) error {
				<-stop
				return nil
			}}
			renewal := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := runApplicationServices(t.Context(), web, func(context.Context) { close(renewal) }, applicationTestFactory, func() (applicationControl, error) { return control, nil })
				done <- err
			}()
			if ready {
				close(web.ready)
				<-renewal
			}
			close(stop)
			select {
			case err := <-done:
				if !errors.Is(err, localreset.ErrUnavailable) {
					t.Fatal("本机安全通道异常退出被当作正常状态")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("本机通道退出后 HTTP 生命周期未结束")
			}
			if control.closed.Load() != 1 {
				t.Fatal("本机通道没有关闭")
			}
		})
	}
}
