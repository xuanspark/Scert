package httpapi

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func (s *Server) connectSSHAfterReload(ctx context.Context, item deployment.Server) (deployment.Client, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		client, err := s.connectSSHForKey(ctx, item)
		if err == nil {
			return client, nil
		}
		var networkError *net.OpError
		if attempt >= 4 || !errors.As(err, &networkError) || networkError.Op != "dial" || !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, err
		}
		timer := time.NewTimer((100 * time.Millisecond) << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
