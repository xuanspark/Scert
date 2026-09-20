package localreset

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testSession struct {
	selected atomic.Int32
	verified atomic.Int32
	resets   atomic.Int32
	closed   atomic.Int32
}

func (s *testSession) Methods() []Method { return []Method{{ID: "recovery", Label: "恢复码"}} }
func (s *testSession) Select(ctx context.Context, method string) error {
	if ctx.Err() != nil || method != "recovery" {
		return ErrProtocol
	}
	s.selected.Add(1)
	return nil
}
func (s *testSession) Verify(ctx context.Context, code string) error {
	if ctx.Err() != nil || s.selected.Load() != 1 || code != "test-code" {
		return ErrProtocol
	}
	s.verified.Add(1)
	return nil
}
func (s *testSession) Reset(ctx context.Context, password string) error {
	if ctx.Err() != nil || s.verified.Load() != 1 || password != "Example123!" {
		return ErrProtocol
	}
	s.resets.Add(1)
	return nil
}
func (s *testSession) Close() { s.closed.Add(1) }

func testConnection(t *testing.T, timeout time.Duration) (*Server, net.Conn, *testSession, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	server := newServer(nil, func(net.Conn) (Peer, error) { return Peer{Identity: "test"}, nil }, nil)
	server.timeout = timeout
	session := &testSession{}
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(done)
		defer serverConn.Close()
		server.handle(ctx, serverConn, func(context.Context, Peer) (Session, error) { return session, nil })
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("connection did not stop")
		}
	})
	return server, clientConn, session, done
}

func TestProtocolRoundTrip(t *testing.T) {
	_, conn, session, done := testConnection(t, time.Second)
	client, err := Begin(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	methods := client.Methods()
	methods[0].ID = "mutated"
	if client.Methods()[0].ID != "recovery" {
		t.Fatal("methods leaked mutable state")
	}
	if err := client.Select("recovery"); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify("test-code"); err != nil {
		t.Fatal(err)
	}
	if err := client.Reset("Example123!"); err != nil {
		t.Fatal(err)
	}
	if err := client.Reset("Example123!"); err == nil {
		t.Fatal("replay accepted")
	}
	<-done
	if session.resets.Load() != 1 || session.closed.Load() != 1 {
		t.Fatal("session not consumed and closed once")
	}
}

func TestProtocolRejectsInvalidFrames(t *testing.T) {
	cases := map[string]func(string) string{
		"invalid-json": func(string) string { return "{broken}\n" },
		"oversized":    func(string) string { return strings.Repeat("x", MaxFrameSize+1) + "\n" },
		"wrong-version": func(n string) string {
			return `{"version":9,"nonce":"` + n + `","action":"select","method":"recovery"}` + "\n"
		},
		"wrong-nonce": func(string) string {
			return `{"version":1,"nonce":"wrong","action":"select","method":"recovery"}` + "\n"
		},
		"skip-verification": func(n string) string {
			return `{"version":1,"nonce":"` + n + `","action":"reset","password":"Example123!"}` + "\n"
		},
		"unknown-field": func(n string) string {
			return `{"version":1,"nonce":"` + n + `","action":"select","method":"recovery","extra":true}` + "\n"
		},
		"trailing-object": func(n string) string {
			return `{"version":1,"nonce":"` + n + `","action":"select","method":"recovery"}{}` + "\n"
		},
		"wrong-method": func(n string) string {
			return `{"version":1,"nonce":"` + n + `","action":"select","method":"missing"}` + "\n"
		},
		"mixed-payload": func(n string) string {
			return `{"version":1,"nonce":"` + n + `","action":"select","method":"recovery","password":"Example123!"}` + "\n"
		},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, conn, session, done := testConnection(t, time.Second)
			reader := bufio.NewReaderSize(conn, MaxFrameSize+1)
			var challenge Response
			if err := readFrame(reader, &challenge); err != nil {
				t.Fatal(err)
			}
			go func() { _, _ = io.WriteString(conn, payload(challenge.Nonce)) }()
			var response Response
			if err := readFrame(reader, &response); err != nil || response.OK {
				t.Fatalf("accepted malformed request: %+v %v", response, err)
			}
			<-done
			if session.resets.Load() != 0 || session.closed.Load() != 1 {
				t.Fatal("invalid request reached reset or leaked session")
			}
		})
	}
}

func TestProtocolRotatesNonce(t *testing.T) {
	_, conn, session, done := testConnection(t, time.Second)
	reader := bufio.NewReaderSize(conn, MaxFrameSize+1)
	var first, second, failure Response
	if err := readFrame(reader, &first); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, Request{Version: Version, Nonce: first.Nonce, Action: "select", Method: "recovery"}); err != nil {
		t.Fatal(err)
	}
	if err := readFrame(reader, &second); err != nil || second.Nonce == first.Nonce {
		t.Fatal("nonce not rotated")
	}
	if err := writeFrame(conn, Request{Version: Version, Nonce: first.Nonce, Action: "verify", Code: "test-code"}); err != nil {
		t.Fatal(err)
	}
	if err := readFrame(reader, &failure); err != nil || failure.OK {
		t.Fatal("replayed nonce accepted")
	}
	<-done
	if session.verified.Load() != 0 {
		t.Fatal("replay reached verification")
	}
}

func TestProtocolDeadlineClosesIdleConnection(t *testing.T) {
	_, conn, session, done := testConnection(t, 30*time.Millisecond)
	var response Response
	if err := readFrame(bufio.NewReaderSize(conn, MaxFrameSize+1), &response); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle connection survived deadline")
	}
	if session.closed.Load() != 1 {
		t.Fatal("expired session leaked")
	}
}

func TestProtocolVerificationFailureIsTerminal(t *testing.T) {
	_, conn, session, done := testConnection(t, time.Second)
	client, err := Begin(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Select("recovery"); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify("wrong-code"); err == nil {
		t.Fatal("wrong code accepted")
	}
	if err := client.Reset("Example123!"); err == nil {
		t.Fatal("failed session reused")
	}
	<-done
	if session.resets.Load() != 0 {
		t.Fatal("verification bypassed")
	}
}

func TestFailureWindowExpires(t *testing.T) {
	server := newServer(nil, nil, nil)
	now := time.Now()
	server.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !server.permit("peer") {
			t.Fatal("premature limit")
		}
		server.failed("peer")
	}
	if server.permit("peer") {
		t.Fatal("failure limit bypassed")
	}
	now = now.Add(time.Minute)
	if !server.permit("peer") {
		t.Fatal("failure limit did not expire")
	}
}

func TestClientCancellationClosesConnection(t *testing.T) {
	_, conn, session, done := testConnection(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	client, err := Begin(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close connection")
	}
	if session.closed.Load() != 1 {
		t.Fatal("cancelled session leaked")
	}
}

func TestFactoryErrorDoesNotDiscloseDetails(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	server := newServer(nil, func(net.Conn) (Peer, error) { return Peer{Identity: "test"}, nil }, nil)
	go func() {
		defer left.Close()
		server.handle(context.Background(), left, func(context.Context, Peer) (Session, error) {
			return nil, errors.New("secret detail")
		})
	}()
	data, err := io.ReadAll(right)
	if err != nil || strings.Contains(string(data), "secret") || !strings.Contains(string(data), "UNAVAILABLE") {
		t.Fatalf("unsafe response: %s %v", data, err)
	}
}

func TestUnauthorizedPeerCannotCreateSession(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	server := newServer(nil, func(net.Conn) (Peer, error) { return Peer{}, ErrUnauthorized }, nil)
	var calls atomic.Int32
	go func() {
		defer left.Close()
		server.handle(context.Background(), left, func(context.Context, Peer) (Session, error) {
			calls.Add(1)
			return &testSession{}, nil
		})
	}()
	data, err := io.ReadAll(right)
	if err != nil || !strings.Contains(string(data), "UNAUTHORIZED") || calls.Load() != 0 {
		t.Fatal("unauthorized peer reached factory")
	}
}
