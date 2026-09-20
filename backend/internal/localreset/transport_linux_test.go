//go:build linux

package localreset

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixSocketRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.sock")
	server, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe socket permissions: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	session := &testSession{}
	go func() {
		done <- server.Serve(ctx, func(context.Context, Peer) (Session, error) { return session, nil })
	}()
	conn, err := Dial(ctx, path, uint32(os.Geteuid()))
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
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if session.resets.Load() != 1 || session.closed.Load() != 1 {
		t.Fatal("session lifecycle incomplete")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned socket not cleaned up")
	}
}

func TestUnixRejectsUnsafeParent(t *testing.T) {
	for _, mode := range []os.FileMode{0777, 0770} {
		t.Run(mode.String(), func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, mode); err != nil {
				t.Fatal(err)
			}
			if server, err := Listen(filepath.Join(parent, "reset.sock")); err == nil {
				_ = server.Close()
				t.Fatal("writable parent accepted")
			}
		})
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	if server, err := Listen(filepath.Join(link, "reset.sock")); err == nil {
		_ = server.Close()
		t.Fatal("symlink parent accepted")
	}
}

func TestUnixRejectsUnsafePathsWithoutOverwriting(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "reset.sock")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if server, err := Listen(path); err == nil {
		_ = server.Close()
		t.Fatal("regular file replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve" {
		t.Fatal("existing file changed")
	}
	for _, path := range []string{"relative.sock", "tcp://127.0.0.1:80", parent + "/../reset.sock"} {
		if server, err := Listen(path); err == nil {
			_ = server.Close()
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
}

func TestUnixActiveSocketCannotBeReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.sock")
	first, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Listen(path); err == nil {
		_ = second.Close()
		t.Fatal("active socket replaced")
	}
	if _, err := Dial(context.Background(), path, uint32(os.Geteuid())+1); err == nil {
		t.Fatal("wrong service identity accepted")
	}
}

func TestUnixStaleOwnedSocketCanBeRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
}

func TestUnixClosePreservesReplacedSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.sock")
	server, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replacement not detected: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatal("replacement removed")
	}
}
