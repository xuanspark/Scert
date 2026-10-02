package cli

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

type readyTestHTTP struct {
	applicationTestHTTP
	entered chan struct{}
}

func (h *readyTestHTTP) ListenAndServe() error {
	close(h.entered)
	return h.applicationTestHTTP.ListenAndServe()
}

func TestApplicationReadyLifecycle(t *testing.T) {
	for _, scenario := range []string{"running", "notify-failure", "startup-failure", "cancel-starting"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			web := &readyTestHTTP{
				applicationTestHTTP: applicationTestHTTP{ready: make(chan struct{}), exit: make(chan error, 2)},
				entered:             make(chan struct{}),
			}
			t.Cleanup(func() { web.exit <- http.ErrServerClosed })
			control := &applicationTestControl{serve: func(ctx context.Context, _ localreset.Factory) error {
				<-ctx.Done()
				return nil
			}}
			expected := errors.New("启动检查失败")
			var notifications atomic.Int32
			var renewals atomic.Int32
			renewed := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := runApplicationServices(ctx, web, func(context.Context) {
					renewals.Add(1)
					if notifications.Load() != 1 {
						t.Error("未完成监听通知就启动了续期")
					}
					close(renewed)
				}, func() error {
					notifications.Add(1)
					if scenario == "notify-failure" {
						return expected
					}
					return nil
				}, applicationTestFactory, func() (applicationControl, error) { return control, nil })
				done <- err
			}()
			select {
			case <-web.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("网络服务未启动")
			}
			if notifications.Load() != 0 {
				t.Fatal("尚未进入监听就报告成功")
			}
			switch scenario {
			case "startup-failure":
				web.exit <- expected
			case "cancel-starting":
				cancel()
			default:
				close(web.ready)
			}
			if scenario == "running" {
				select {
				case <-renewed:
				case <-time.After(3 * time.Second):
					t.Fatal("通知完成后未启动续期")
				}
				cancel()
			}
			select {
			case err := <-done:
				if scenario == "notify-failure" || scenario == "startup-failure" {
					if !errors.Is(err, expected) {
						t.Fatalf("丢失启动失败原因：%v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("启动失败或取消后未退出")
			}
			if control.closed.Load() != 1 || (scenario != "running" && renewals.Load() != 0) {
				t.Fatal("启动失败后的资源或续期状态不正确")
			}
			want := int32(0)
			if scenario == "running" || scenario == "notify-failure" {
				want = 1
			}
			if notifications.Load() != want {
				t.Fatal("就绪通知次数不正确")
			}
		})
	}
}
