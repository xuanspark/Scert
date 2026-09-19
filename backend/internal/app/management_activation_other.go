//go:build !linux

package app

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
)

func newManagementActivator(_ config.Config, server *httpapi.Server) (managementActivator, func(context.Context) error, error) {
	return directManagementActivator{server: server}, nil, nil
}
