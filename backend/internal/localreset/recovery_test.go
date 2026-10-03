package localreset

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type recoveryTestSession struct {
	testSession
	lists      atomic.Int32
	inspects   atomic.Int32
	fail       bool
	oversized  bool
	inspection *deployment.RecoveryInspection
}

func (*recoveryTestSession) RecoveryAvailable() bool { return true }
func (s *recoveryTestSession) ListRecoveries(context.Context, string) (deployment.PendingRecoveryPage, error) {
	s.lists.Add(1)
	return deployment.PendingRecoveryPage{Items: []deployment.PendingRecovery{{ID: "tx-1", ServerID: "s1"}}}, nil
}
func (s *recoveryTestSession) InspectRecovery(_ context.Context, id string) (deployment.RecoveryInspection, error) {
	s.inspects.Add(1)
	if s.inspection != nil {
		return *s.inspection, nil
	}
	result := deployment.RecoveryInspection{TransactionID: id, ServerID: "s1", ObservedAt: time.Now(),
		Files: make([]deployment.RecoveryFileObservation, 9), Locks: make([]deployment.RecoveryLockObservation, 3)}
	if s.fail {
		return result, errors.New("secret-error")
	}
	if s.oversized {
		result.Files[0].Path = strings.Repeat("x", MaxFrameSize)
	}
	return result, nil
}

func recoveryConnection(t *testing.T, session Session) (*Server, net.Conn, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	server := newServer(nil, func(net.Conn) (Peer, error) { return Peer{Identity: "test"}, nil }, nil)
	server.timeout = 5 * time.Second
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
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
			t.Error("recovery session did not exit")
		}
	})
	return server, clientConn, done
}

func TestRecoveryProtocolRoundTrip(t *testing.T) {
	session := &recoveryTestSession{}
	server, conn, done := recoveryConnection(t, session)
	client, err := Begin(t.Context(), conn)
	if err != nil || !client.RecoveryAvailable() {
		t.Fatalf("begin: %v", err)
	}
	page, err := client.ListRecoveries("")
	if err != nil || len(page.Items) != 1 {
		t.Fatal(err)
	}
	result, err := client.InspectRecovery(page.Items[0].ID)
	if err != nil || result.HistoricalEvidence || result.TransactionID != "tx-1" {
		t.Fatalf("inspection: %+v, %v", result, err)
	}
	_ = client.Close()
	<-done
	if session.resets.Load() != 0 || session.selected.Load() != 0 || session.verified.Load() != 0 ||
		session.closed.Load() != 1 || session.lists.Load() != 1 || session.inspects.Load() != 1 || len(server.failures) != 0 {
		t.Fatal("read-only flow mutated reset state, leaked session, or penalized clean close")
	}
}

func TestRecoveryProtocolRejectsReplayAndCrossFlow(t *testing.T) {
	for _, scenario := range []string{"replay", "select", "verify", "reset", "mixed", "bad-id"} {
		t.Run(scenario, func(t *testing.T) {
			session := &recoveryTestSession{}
			_, conn, done := recoveryConnection(t, session)
			reader := bufio.NewReaderSize(conn, MaxFrameSize+1)
			var response Response
			if err := readFrame(reader, &response); err != nil {
				t.Fatal(err)
			}
			oldNonce := response.Nonce
			if err := writeFrame(conn, Request{Version: Version, Nonce: oldNonce, Action: "recovery_list"}); err != nil {
				t.Fatal(err)
			}
			if err := readFrame(reader, &response); err != nil {
				t.Fatal(err)
			}
			request := Request{Version: Version, Nonce: response.Nonce, Action: "recovery_inspect", TransactionID: "tx-1"}
			switch scenario {
			case "replay":
				request.Nonce = oldNonce
			case "select":
				request = Request{Version: Version, Nonce: response.Nonce, Action: "select", Method: "recovery"}
			case "verify":
				request = Request{Version: Version, Nonce: response.Nonce, Action: "verify", Code: "test-code"}
			case "reset":
				request = Request{Version: Version, Nonce: response.Nonce, Action: "reset", Password: "Example123!"}
			case "mixed":
				request.Password = "secret-password"
			case "bad-id":
				request.TransactionID = "../bad"
			}
			if err := writeFrame(conn, request); err != nil {
				t.Fatal(err)
			}
			response = Response{}
			if err := readFrame(reader, &response); err != nil || response.OK || response.Inspection != nil {
				t.Fatalf("accepted: %+v %v", response, err)
			}
			<-done
			if session.inspects.Load() != 0 || session.resets.Load() != 0 || session.selected.Load() != 0 || session.verified.Load() != 0 {
				t.Fatal("invalid request reached business operation")
			}
		})
	}
}

func TestRecoveryClientAndResetMutuallyExclusive(t *testing.T) {
	for _, first := range []string{"reset", "recovery"} {
		t.Run(first, func(t *testing.T) {
			session := &recoveryTestSession{}
			_, conn, _ := recoveryConnection(t, session)
			client, err := Begin(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if first == "reset" {
				if err := client.Select("recovery"); err != nil {
					t.Fatal(err)
				}
				if _, err := client.ListRecoveries(""); err == nil {
					t.Fatal("cross-flow accepted")
				}
			} else {
				if _, err := client.ListRecoveries(""); err != nil {
					t.Fatal(err)
				}
				if err := client.Select("recovery"); err == nil {
					t.Fatal("cross-flow accepted")
				}
			}
		})
	}
}

func TestRecoveryProtocolFailureReturnsNoPartialData(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		session := &recoveryTestSession{fail: !oversized, oversized: oversized}
		_, conn, _ := recoveryConnection(t, session)
		client, err := Begin(t.Context(), conn)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.InspectRecovery("tx-1")
		if err == nil || result.TransactionID != "" || len(result.Files) != 0 || strings.Contains(err.Error(), "secret") {
			t.Fatal("failure leaked partial data")
		}
		_ = client.Close()
	}
}

func TestRecoveryProtocolRequestLimit(t *testing.T) {
	session := &recoveryTestSession{}
	_, conn, done := recoveryConnection(t, session)
	client, err := Begin(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i := 0; i < MaxRecoveryRequests; i++ {
		if _, err := client.ListRecoveries(""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.ListRecoveries(""); err == nil {
		t.Fatal("request limit ignored")
	}
	<-done
	if session.lists.Load() != MaxRecoveryRequests {
		t.Fatal("extra list reached service")
	}
}
