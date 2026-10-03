package localreset

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type dispositionTestSession struct {
	recoveryTestSession
	prepares atomic.Int32
	executes atomic.Int32
	queries  atomic.Int32
	prepared RecoveryPreparation
	result   deployment.RecoveryDispositionSummary
	fail     bool
}

func (*dispositionTestSession) DispositionAvailable() bool { return true }
func (s *dispositionTestSession) PrepareRecovery(_ context.Context, id string, action deployment.RecoveryAction) (RecoveryPreparation, error) {
	s.prepares.Add(1)
	now := time.Now().UTC()
	s.prepared = RecoveryPreparation{DispositionID: strings.Repeat("b", 64), Token: strings.Repeat("c", 64),
		Summary: deployment.RecoveryReviewSummary{TransactionID: id, ServerID: "server-1", Phase: "reload_attempted", Action: action, HistoricalEvidence: true, ObservedAt: now, ExpiresAt: now.Add(time.Minute)}}
	return s.prepared, nil
}
func (s *dispositionTestSession) ExecuteRecovery(_ context.Context, id, token string) (deployment.RecoveryDispositionSummary, error) {
	s.executes.Add(1)
	if id != s.prepared.DispositionID || token != s.prepared.Token || s.fail {
		return deployment.RecoveryDispositionSummary{}, errors.New("secret-executor-error")
	}
	now := time.Now().UTC()
	results := map[deployment.RecoveryAction]string{deployment.RecoveryActionRecord: "observed", deployment.RecoveryActionCommit: "committed", deployment.RecoveryActionRollback: "rolled_back"}
	s.result = deployment.RecoveryDispositionSummary{ID: id, TransactionID: s.prepared.Summary.TransactionID, ServerID: "server-1", Action: s.prepared.Summary.Action, Status: "succeeded", ResultCode: results[s.prepared.Summary.Action], CreatedAt: now, UpdatedAt: now}
	s.prepared = RecoveryPreparation{}
	return s.result, nil
}
func (s *dispositionTestSession) GetRecoveryDisposition(context.Context, string) (deployment.RecoveryDispositionSummary, error) {
	s.queries.Add(1)
	return s.result, nil
}

func TestDispositionProtocolThreeActionsAndQuery(t *testing.T) {
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionRecord, deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		t.Run(string(action), func(t *testing.T) {
			session := &dispositionTestSession{}
			_, conn, done := recoveryConnection(t, session)
			client, err := Begin(t.Context(), conn)
			if err != nil || !client.DispositionAvailable() {
				t.Fatalf("capability: %v", err)
			}
			prepared, err := client.PrepareRecovery("tx-1", action)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token)
			if err != nil || result.Action != action || result.Status != "succeeded" {
				t.Fatalf("execute: %+v %v", result, err)
			}
			query, err := client.GetRecoveryDisposition(result.ID)
			if err != nil || query != result {
				t.Fatalf("query: %+v %v", query, err)
			}
			if _, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token); err == nil {
				t.Fatal("replay accepted")
			}
			_ = client.Close()
			<-done
			if session.prepares.Load() != 1 || session.executes.Load() != 2 || session.queries.Load() != 1 || session.resets.Load() != 0 {
				t.Fatal("unexpected business calls")
			}
		})
	}
}

func TestDispositionRequestFieldIsolation(t *testing.T) {
	nonce := strings.Repeat("a", 64)
	requests := []Request{
		{Action: "recovery_list"}, {Action: "recovery_inspect", TransactionID: "tx-1"},
		{Action: "recovery_prepare", TransactionID: "tx-1", RecoveryAction: deployment.RecoveryActionRecord},
		{Action: "recovery_execute", DispositionID: strings.Repeat("b", 64), ConfirmationToken: strings.Repeat("c", 64)},
		{Action: "recovery_result", DispositionID: strings.Repeat("b", 64)},
		{Action: "recovery_reconcile_prepare", DispositionID: strings.Repeat("b", 64)},
	}
	fields := []struct {
		name    string
		set     func(*Request)
		allowed map[string]bool
	}{
		{"method", func(r *Request) { r.Method = "email" }, nil},
		{"password", func(r *Request) { r.Password = "secret" }, nil},
		{"code", func(r *Request) { r.Code = "12345678" }, nil},
		{"cursor", func(r *Request) { r.Cursor = "tx-0" }, map[string]bool{"recovery_list": true}},
		{"transaction", func(r *Request) { r.TransactionID = "tx-1" }, map[string]bool{"recovery_inspect": true, "recovery_prepare": true}},
		{"action", func(r *Request) { r.RecoveryAction = deployment.RecoveryActionRecord }, map[string]bool{"recovery_prepare": true}},
		{"id", func(r *Request) { r.DispositionID = strings.Repeat("b", 64) }, map[string]bool{"recovery_execute": true, "recovery_result": true, "recovery_reconcile_prepare": true}},
		{"token", func(r *Request) { r.ConfirmationToken = strings.Repeat("c", 64) }, map[string]bool{"recovery_execute": true}},
	}
	for _, request := range requests {
		request.Version, request.Nonce = Version, nonce
		if !validRecoveryRequest(request, nonce) {
			t.Fatalf("valid request rejected: %s", request.Action)
		}
		for _, field := range fields {
			if field.allowed[request.Action] {
				continue
			}
			mixed := request
			field.set(&mixed)
			if validRecoveryRequest(mixed, nonce) {
				t.Fatalf("mixed %s/%s accepted", request.Action, field.name)
			}
		}
		if validRecoveryRequest(request, strings.Repeat("d", 64)) {
			t.Fatal("old nonce accepted")
		}
	}
	for _, field := range fields[5:] {
		request := Request{Version: Version, Nonce: nonce, Action: "select", Method: "email"}
		field.set(&request)
		if validRequest(request, nonce, "select") {
			t.Fatalf("reset accepts %s", field.name)
		}
	}
	for _, bad := range []string{"", ".", "..", strings.Repeat("0", 64), strings.Repeat("A", 64), strings.Repeat("b", 65)} {
		if validRecoverySecret(bad) {
			t.Fatal("bad secret accepted")
		}
	}
}

func TestDispositionReplayNonceAndMixedPayloadNeverExecute(t *testing.T) {
	for _, mode := range []string{"old-nonce", "password", "cursor", "transaction", "action", "prepare-token", "query-token"} {
		t.Run(mode, func(t *testing.T) {
			session := &dispositionTestSession{}
			_, conn, done := recoveryConnection(t, session)
			reader := bufio.NewReaderSize(conn, MaxFrameSize+1)
			var first Response
			if err := readFrame(reader, &first); err != nil {
				t.Fatal(err)
			}
			if err := writeFrame(conn, Request{Version: Version, Nonce: first.Nonce, Action: "recovery_prepare", TransactionID: "tx-1", RecoveryAction: deployment.RecoveryActionRecord}); err != nil {
				t.Fatal(err)
			}
			var prepared Response
			if err := readFrame(reader, &prepared); err != nil {
				t.Fatal(err)
			}
			request := Request{Version: Version, Nonce: prepared.Nonce, Action: "recovery_execute", DispositionID: prepared.Preparation.DispositionID, ConfirmationToken: prepared.Preparation.Token}
			switch mode {
			case "old-nonce":
				request.Nonce = first.Nonce
			case "password":
				request.Password = "secret"
			case "cursor":
				request.Cursor = "tx-0"
			case "transaction":
				request.TransactionID = "tx-1"
			case "action":
				request.RecoveryAction = deployment.RecoveryActionRollback
			case "prepare-token":
				request.Action, request.TransactionID, request.RecoveryAction = "recovery_prepare", "tx-1", deployment.RecoveryActionRecord
			case "query-token":
				request.Action = "recovery_result"
			}
			if err := writeFrame(conn, request); err != nil {
				t.Fatal(err)
			}
			var response Response
			if err := readFrame(reader, &response); err != nil || response.OK || response.Code != "INVALID_REQUEST" {
				t.Fatalf("response: %+v %v", response, err)
			}
			<-done
			if session.executes.Load() != 0 || session.queries.Load() != 0 || session.prepares.Load() != 1 {
				t.Fatal("invalid request reached service")
			}
		})
	}
}

func TestDispositionClientRejectsMalformedPrepare(t *testing.T) {
	for _, mode := range []string{"token", "id", "transaction", "action", "lifetime", "missing-evidence", "mixed-result", "mixed-page", "capability"} {
		t.Run(mode, func(t *testing.T) {
			server, conn := net.Pipe()
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(2 * time.Second))
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				_ = writeFrame(server, Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("a", 64), Methods: []Method{{ID: "email"}}, RecoveryAvailable: true, DispositionAvailable: true})
				var request Request
				if readFrame(bufio.NewReaderSize(server, MaxFrameSize+1), &request) != nil {
					return
				}
				session := &dispositionTestSession{}
				value, _ := session.PrepareRecovery(t.Context(), "tx-1", deployment.RecoveryActionCommit)
				response := Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("b", 64), Preparation: &value}
				switch mode {
				case "token":
					value.Token = "bad"
				case "id":
					value.DispositionID = strings.Repeat("0", 64)
				case "transaction":
					value.Summary.TransactionID = "tx-2"
				case "action":
					value.Summary.Action = deployment.RecoveryActionRollback
				case "lifetime":
					value.Summary.ExpiresAt = value.Summary.ObservedAt.Add(time.Hour)
				case "missing-evidence":
					value.Summary.HistoricalEvidence = false
				case "mixed-result":
					response.Disposition = &deployment.RecoveryDispositionSummary{}
				case "mixed-page":
					response.RecoveryPage = &deployment.PendingRecoveryPage{}
				case "capability":
					response.DispositionAvailable = true
				}
				_ = writeFrame(server, response)
			}()
			client, err := Begin(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			if value, err := client.PrepareRecovery("tx-1", deployment.RecoveryActionCommit); err == nil || value.Token != "" {
				t.Fatal("malformed preparation returned")
			}
			_ = client.Close()
			<-done
		})
	}
}

func TestDispositionFieldsRejectedInInspectionParts(t *testing.T) {
	inspection := longInspectionFixture()
	var raw bytes.Buffer
	if err := writeRecoveryResponse(t.Context(), &raw, Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("a", 64), Inspection: &inspection}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"preparation", "disposition", "capability"} {
		for _, index := range []int{0, 1} {
			lines := bytes.Split(bytes.TrimSuffix(raw.Bytes(), []byte{'\n'}), []byte{'\n'})
			var changed Response
			if err := json.Unmarshal(lines[index], &changed); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "preparation":
				changed.Preparation = &RecoveryPreparation{}
			case "disposition":
				changed.Disposition = &deployment.RecoveryDispositionSummary{}
			case "capability":
				changed.DispositionAvailable = true
			}
			lines[index], _ = json.Marshal(changed)
			reader := bufio.NewReaderSize(bytes.NewReader(append(bytes.Join(lines, []byte{'\n'}), '\n')), MaxFrameSize+1)
			var first Response
			if err := readFrame(reader, &first); err != nil {
				t.Fatal(err)
			}
			if value, err := readInspectionParts(reader, first); err == nil || value != nil {
				t.Fatalf("mixed fragment %s/%d accepted", field, index)
			}
		}
	}
}

func TestDispositionQueryAfterReconnectDoesNotExecute(t *testing.T) {
	session := &dispositionTestSession{}
	_, conn, done := recoveryConnection(t, session)
	client, err := Begin(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := client.PrepareRecovery("tx-1", deployment.RecoveryActionRecord)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	<-done
	next := &dispositionTestSession{result: session.result}
	_, conn, _ = recoveryConnection(t, next)
	client, err = Begin(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if result, err := client.GetRecoveryDisposition(prepared.DispositionID); err != nil || result.Status != "succeeded" {
		t.Fatalf("query: %+v %v", result, err)
	}
	if next.prepares.Load() != 0 || next.executes.Load() != 0 || next.queries.Load() != 1 {
		t.Fatal("query replayed operation")
	}
}
