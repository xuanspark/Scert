package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type reloadTestFactory struct {
	connect func(context.Context, deployment.Server) (deployment.Client, error)
}

func (f reloadTestFactory) Connect(ctx context.Context, item deployment.Server) (deployment.Client, error) {
	return f.connect(ctx, item)
}

type reloadTestClient struct{ deployment.Client }

func refusedReloadConnection() error {
	return fmt.Errorf("%w", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})
}

func TestSSHAfterReloadRetriesOnlyConnectionRefusal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		failures int
		calls    int
	}{
		{"ready", nil, 0, 1},
		{"recovered", refusedReloadConnection(), 2, 3},
		{"exhausted", refusedReloadConnection(), 5, 5},
		{"authentication", errors.New("authentication"), 1, 1},
		{"fingerprint", errors.New("fingerprint"), 1, 1},
		{"timeout", &net.OpError{Op: "dial", Err: syscall.ETIMEDOUT}, 1, 1},
		{"handshake", &net.OpError{Op: "read", Err: syscall.ECONNREFUSED}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			wantClient := &reloadTestClient{}
			server := &Server{deploymentFactory: reloadTestFactory{connect: func(ctx context.Context, item deployment.Server) (deployment.Client, error) {
				calls++
				if item.AuthMethod != deployment.SSHAuthPrivateKey || item.HostKeyFingerprint != "SHA256:registered" {
					t.Fatal(item.AuthMethod, item.HostKeyFingerprint)
				}
				if calls <= tc.failures {
					return nil, tc.err
				}
				return wantClient, nil
			}}}
			client, err := server.connectSSHAfterReload(t.Context(), deployment.Server{
				AuthMethod: deployment.SSHAuthPrivateKey, HostKeyFingerprint: "SHA256:registered",
			})
			if calls != tc.calls {
				t.Fatalf("calls=%d; want=%d", calls, tc.calls)
			}
			if tc.calls <= tc.failures {
				if !errors.Is(err, tc.err) || client != nil {
					t.Fatal(client, err)
				}
			} else if err != nil || client != wantClient {
				t.Fatal(client, err)
			}
		})
	}
}

func TestSSHAfterReloadHonorsCancellation(t *testing.T) {
	for _, cancelBeforeConnect := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		server := &Server{deploymentFactory: reloadTestFactory{connect: func(context.Context, deployment.Server) (deployment.Client, error) {
			calls++
			cancel()
			return nil, refusedReloadConnection()
		}}}
		if cancelBeforeConnect {
			cancel()
		}
		client, err := server.connectSSHAfterReload(ctx, deployment.Server{})
		cancel()
		if !errors.Is(err, context.Canceled) || client != nil {
			t.Fatal(client, err)
		}
		wantCalls := 1
		if cancelBeforeConnect {
			wantCalls = 0
		}
		if calls != wantCalls {
			t.Fatalf("calls=%d; want=%d", calls, wantCalls)
		}
	}
}
