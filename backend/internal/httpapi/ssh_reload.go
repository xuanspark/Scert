package httpapi

import (
	"context"
	"errors"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func (s *Server) connectSSHAfterReload(ctx context.Context, item deployment.Server) (deployment.Client, error) {
	_ = ctx
	_ = item
	return nil, errors.New("服务器仅支持 SSH 密码认证，已禁用 SSH 密钥登录")
}
