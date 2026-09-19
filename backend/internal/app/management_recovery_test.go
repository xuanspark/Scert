package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
)

func TestManagementRecoveryCloseCancelsAndWaits(t *testing.T) {
	application := &App{}
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseRecovery := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseRecovery)
	application.startManagementRecovery(func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 100*time.Second {
			return errors.New("恢复尝试必须有超时边界")
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	})
	t.Cleanup(func() {
		releaseRecovery()
		if err := application.Close(); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("恢复协程没有开始执行")
	}
	closed := make(chan error, 1)
	go func() { closed <- application.Close() }()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("关闭应用没有取消恢复尝试")
	}
	select {
	case err := <-closed:
		t.Fatal("恢复尝试退出前不能完成应用关闭", err)
	default:
	}
	releaseRecovery()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("恢复退出后应用关闭没有完成")
	}
}

func TestManagementRecoveryCloseInterruptsRetryWait(t *testing.T) {
	application := &App{}
	returned := make(chan struct{})
	application.startManagementRecovery(func(context.Context) error {
		close(returned)
		return errors.New("注入临时恢复失败")
	})
	t.Cleanup(func() { _ = application.Close() })
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("首次恢复未执行")
	}
	closed := make(chan error, 1)
	go func() { closed <- application.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭应用不应等待整分钟的重试间隔")
	}
}

func TestManagementRecoveryNilDoesNotStartWorker(t *testing.T) {
	application := &App{}
	application.startManagementRecovery(nil)
	if application.managementStop != nil || application.managementDone != nil {
		t.Fatal("未配置恢复逻辑时不应启动协程")
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagementRecoveryWaitsForHTTPS(t *testing.T) {
	application := &App{HTTP: httpapi.New(httpapi.Dependencies{})}
	called := make(chan struct{}, 1)
	application.startManagementRecovery(func(context.Context) error {
		called <- struct{}{}
		return nil
	})
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("HTTPS 尚未监听时不能开始恢复")
	default:
	}
}
