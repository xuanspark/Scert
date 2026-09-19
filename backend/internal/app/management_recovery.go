package app

import (
	"context"
	"time"
)

func (a *App) startManagementRecovery(recover func(context.Context) error) {
	if recover == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.managementStop = cancel
	a.managementDone = make(chan struct{})
	go func() {
		defer close(a.managementDone)
		for {
			attempt, stop := context.WithTimeout(ctx, 100*time.Second)
			err := recover(attempt)
			stop()
			if ctx.Err() != nil {
				return
			}
			if err != nil && a.Logger != nil {
				a.Logger.Error("管理证书协调恢复尚未完成，保持续期保护", "error", err)
			}
			if err := waitForManagementRetry(ctx, time.Minute); err != nil {
				return
			}
		}
	}()
}
