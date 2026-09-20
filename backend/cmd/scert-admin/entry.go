package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"git.pcux.cn/xuans/Scert/backend/internal/adminconsole"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := adminconsole.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Scert-admin：", err)
		os.Exit(1)
	}
}
