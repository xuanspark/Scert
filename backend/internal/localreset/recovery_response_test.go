package localreset

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func TestRecoveryClientRejectsMalformedResponses(t *testing.T) {
	for _, scenario := range []string{"nonce", "methods", "capability", "missing-page", "large-page", "duplicate-id", "backward-cursor", "mixed-result", "wrong-id", "partial-files", "partial-locks", "missing-time"} {
		t.Run(scenario, func(t *testing.T) {
			serverConn, conn := net.Pipe()
			defer conn.Close()
			defer serverConn.Close()
			_ = serverConn.SetDeadline(time.Now().Add(time.Second))
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer serverConn.Close()
				nonce := strings.Repeat("a", 64)
				if writeFrame(serverConn, Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: nonce,
					Methods: []Method{{ID: "email"}}, RecoveryAvailable: true}) != nil {
					return
				}
				var request Request
				if readFrame(bufio.NewReaderSize(serverConn, MaxFrameSize+1), &request) != nil {
					return
				}
				response := Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("b", 64)}
				if request.Action == "recovery_list" {
					response.RecoveryPage = &deployment.PendingRecoveryPage{Items: []deployment.PendingRecovery{{ID: "tx-1"}}}
				} else {
					response.Inspection = &deployment.RecoveryInspection{TransactionID: "tx-1", ObservedAt: time.Now(),
						Files: make([]deployment.RecoveryFileObservation, 9), Locks: make([]deployment.RecoveryLockObservation, 3)}
				}
				switch scenario {
				case "nonce":
					response.Nonce = nonce
				case "methods":
					response.Methods = []Method{{ID: "email"}}
				case "capability":
					response.RecoveryAvailable = true
				case "missing-page":
					response.RecoveryPage = nil
				case "large-page":
					response.RecoveryPage.Items = make([]deployment.PendingRecovery, 6)
				case "duplicate-id":
					response.RecoveryPage.Items = append(response.RecoveryPage.Items, response.RecoveryPage.Items[0])
				case "backward-cursor":
					response.RecoveryPage.NextCursor = "tx-0"
				case "mixed-result":
					response.Inspection = &deployment.RecoveryInspection{TransactionID: "tx-1"}
				case "wrong-id":
					response.Inspection.TransactionID = "tx-2"
				case "partial-files":
					response.Inspection.Files = response.Inspection.Files[:8]
				case "partial-locks":
					response.Inspection.Locks = response.Inspection.Locks[:2]
				case "missing-time":
					response.Inspection.ObservedAt = time.Time{}
				}
				_ = writeFrame(serverConn, response)
			}()
			client, err := Begin(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			switch scenario {
			case "wrong-id", "partial-files", "partial-locks", "missing-time":
				result, err := client.InspectRecovery("tx-1")
				if err == nil || result.TransactionID != "" {
					t.Fatal("malformed observation accepted")
				}
			default:
				page, err := client.ListRecoveries("")
				if err == nil || len(page.Items) != 0 {
					t.Fatal("malformed list accepted")
				}
			}
			<-done
		})
	}
}

func TestRecoveryProtocolCannotEnterAfterResetSelection(t *testing.T) {
	session := &recoveryTestSession{}
	_, conn, done := recoveryConnection(t, session)
	reader := bufio.NewReaderSize(conn, MaxFrameSize+1)
	var response Response
	if err := readFrame(reader, &response); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, Request{Version: Version, Nonce: response.Nonce, Action: "select", Method: "recovery"}); err != nil {
		t.Fatal(err)
	}
	response = Response{}
	if err := readFrame(reader, &response); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, Request{Version: Version, Nonce: response.Nonce, Action: "recovery_list"}); err != nil {
		t.Fatal(err)
	}
	response = Response{}
	if err := readFrame(reader, &response); err != nil || response.OK {
		t.Fatal("reset switched into inspection")
	}
	<-done
	if session.lists.Load() != 0 || session.inspects.Load() != 0 || session.resets.Load() != 0 {
		t.Fatal("invalid cross-flow reached service")
	}
}
