package httpapi

import (
	"context"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type reloadTestFactory struct {
	connect func(context.Context, deployment.Server) (deployment.Client, error)
}

func (f reloadTestFactory) Connect(ctx context.Context, item deployment.Server) (deployment.Client, error) {
	return f.connect(ctx, item)
}

func TestSSHAfterReloadRejectsUnsupportedAuthentication(t *testing.T) {
	server := &Server{deploymentFactory: reloadTestFactory{connect: func(context.Context, deployment.Server) (deployment.Client, error) {
		t.Fatal("unsupported authentication must not reach the deployment factory")
		return nil, nil
	}}}
	client, err := server.connectSSHAfterReload(t.Context(), deployment.Server{
		AuthMethod:         deployment.SSHAuthMethod("unsupported"),
		HostKeyFingerprint: "SHA256:registered",
	})
	if client != nil || err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("unsupported authentication should be rejected: client=%v err=%v", client, err)
	}
}
