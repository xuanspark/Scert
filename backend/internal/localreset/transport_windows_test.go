//go:build windows

package localreset

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func testWindowsPath() string {
	return fmt.Sprintf(`\\.\pipe\scert-reset-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

func currentWindowsPeer() (Peer, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return Peer{}, err
	}
	defer token.Close()
	return tokenPeer(token)
}

func startTestServer(t *testing.T, server *Server, factory Factory) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, factory) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("server failed to stop")
		}
	})
}

func TestWindowsNamedPipeRoundTrip(t *testing.T) {
	if _, err := currentWindowsPeer(); err != nil {
		t.Skip("requires elevated administrator or SYSTEM")
	}
	path := testWindowsPath()
	server, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	session := &testSession{}
	startTestServer(t, server, func(context.Context, Peer) (Session, error) { return session, nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := Dial(ctx, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	client, err := Begin(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Select("recovery"); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify("test-code"); err != nil {
		t.Fatal(err)
	}
	if err := client.Reset("Example123!"); err != nil {
		t.Fatal(err)
	}
	if session.resets.Load() != 1 {
		t.Fatal("reset not applied")
	}
}

func TestWindowsRejectsNonElevatedPeer(t *testing.T) {
	if _, err := currentWindowsPeer(); err == nil {
		t.Skip("requires non-elevated process")
	}
	path := testWindowsPath()
	server, err := listenPipe(path, "D:P(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	startTestServer(t, server, func(context.Context, Peer) (Session, error) {
		calls.Add(1)
		return &testSession{}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(ctx, conn); err == nil {
		t.Fatal("non-elevated client accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("non-elevated peer reached reset factory")
	}
	if conn, err := Dial(ctx, path, 0); err == nil {
		_ = conn.Close()
		t.Fatal("non-elevated server impersonation accepted")
	}
}

func TestWindowsRejectsRemotePipePaths(t *testing.T) {
	for _, path := range []string{`\\remote\pipe\scert-admin-control`, `tcp://127.0.0.1:80`, `\\.\pipe\`, `\\.\pipe\nested\pipe`} {
		if _, err := pipePath(path); err == nil {
			t.Fatalf("unsafe pipe accepted: %s", path)
		}
	}
	if !strings.Contains(pipeSDDL, "(D;;GA;;;NU)") {
		t.Fatal("network deny ACE missing")
	}
}

func TestWindowsActivePipeCannotBeReplaced(t *testing.T) {
	path := testWindowsPath()
	first, err := listenPipe(path, "D:P(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := listenPipe(path, "D:P(A;;GA;;;WD)")
	if err == nil {
		_ = second.Close()
		t.Fatal("active pipe replaced")
	}
}

func TestServerShutdownClosesIdleConnections(t *testing.T) {
	path := testWindowsPath()
	server, err := listenPipe(path, "D:P(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	server.authorize = func(net.Conn) (Peer, error) { return Peer{Identity: "test"}, nil }
	session := &testSession{}
	startTestServer(t, server, func(context.Context, Peer) (Session, error) { return session, nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := Begin(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Select("recovery"); err == nil {
		t.Fatal("closed server accepted request")
	}
}
