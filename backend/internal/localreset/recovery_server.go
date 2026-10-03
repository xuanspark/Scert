package localreset

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
)

func (s *Server) handleRecovery(ctx context.Context, conn net.Conn, reader *bufio.Reader, peer Peer, session RecoverySession, request Request) {
	for count := 0; count < MaxRecoveryRequests; count++ {
		if ctx.Err() != nil {
			return
		}
		nonce, err := newNonce()
		if err != nil {
			return
		}
		response := Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: nonce}
		switch request.Action {
		case "recovery_list":
			page, readErr := session.ListRecoveries(ctx, request.Cursor)
			err, response.RecoveryPage = readErr, &page
		case "recovery_inspect":
			inspection, readErr := session.InspectRecovery(ctx, request.TransactionID)
			err, response.Inspection = readErr, &inspection
		case "recovery_prepare", "recovery_execute", "recovery_result":
			err = dispatchRecoveryDisposition(ctx, session, request, &response)
		default:
			err = ErrProtocol
		}
		if err != nil || ctx.Err() != nil {
			s.failed(peer.Identity)
			_ = writeFrame(conn, Response{Version: Version, Code: "REJECTED"})
			return
		}
		if writeRecoveryResponse(ctx, conn, response) != nil {
			return
		}
		request = Request{}
		if _, err := reader.Peek(1); errors.Is(err, io.EOF) {
			return
		}
		if readFrame(reader, &request) != nil || !validRecoveryRequest(request, nonce) {
			s.failed(peer.Identity)
			_ = writeFrame(conn, Response{Version: Version, Code: "INVALID_REQUEST"})
			return
		}
	}
	_ = writeFrame(conn, Response{Version: Version, Code: "LIMIT_REACHED"})
}
