package localreset

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func longInspectionFixture() deployment.RecoveryInspection {
	value := deployment.RecoveryInspection{
		TransactionID: strings.Repeat("t", 160), ServerID: "server-1",
		Phase: "recovery_required", Status: "recovery_required", ObservedAt: time.Now().UTC(),
	}
	for _, name := range []string{"certificate", "chain", "key"} {
		path := "/" + strings.Repeat(strings.Repeat("<", 199)+"/", 20)
		path += strings.Repeat("&", 4096-len(path)-len(name)) + name
		for _, suffix := range []string{"", ".scert-tmp-" + value.TransactionID, ".scert-backup-" + value.TransactionID} {
			value.Files = append(value.Files, deployment.RecoveryFileObservation{
				Path: path + suffix, Kind: "target", Exists: true, Digest: strings.Repeat("a", 64), Comparison: "unproven",
			})
		}
		value.Locks = append(value.Locks, deployment.RecoveryLockObservation{Path: path + ".scert-lock", Exists: true, Ownership: "unproven"})
	}
	return value
}

func TestRecoveryInspectionLongPathsRoundTrip(t *testing.T) {
	inspection := longInspectionFixture()
	encoded, err := json.Marshal(inspection)
	if err != nil || len(encoded) <= 250<<10 || len(encoded) > maxInspectionBytes {
		t.Fatalf("fixture does not exercise escaped path bound: %d %v", len(encoded), err)
	}
	session := &recoveryTestSession{inspection: &inspection}
	_, conn, _ := recoveryConnection(t, session)
	client, err := Begin(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	before := client.nonce
	result, err := client.InspectRecovery(inspection.TransactionID)
	if err != nil || !reflect.DeepEqual(result, inspection) {
		t.Fatalf("long observation changed or failed: %v", err)
	}
	if client.nonce == before || session.inspects.Load() != 1 {
		t.Fatal("nonce unchanged or observation repeated")
	}
	if _, err := client.ListRecoveries(""); err != nil || session.lists.Load() != 1 {
		t.Fatalf("next request failed: %v", err)
	}
}

func TestRecoveryInspectionFrameBounds(t *testing.T) {
	inspection := longInspectionFixture()
	response := Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("a", 64), Inspection: &inspection}
	var output bytes.Buffer
	if err := writeRecoveryResponse(t.Context(), &output, response); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), []byte{'\n'})
	if len(lines) < 2 || len(lines) > maxInspectionParts {
		t.Fatalf("unexpected fragment count: %d", len(lines))
	}
	for index, line := range lines {
		var frame Response
		if len(line)+1 > MaxFrameSize || json.Unmarshal(line, &frame) != nil || frame.InspectionPart == nil ||
			frame.InspectionPart.Index != index || frame.InspectionPart.Total != len(lines) || frame.Nonce != response.Nonce {
			t.Fatal("frame exceeds limit or has invalid sequence")
		}
	}
	inspection.Status = strings.Repeat("x", maxInspectionBytes)
	output.Reset()
	if err := writeRecoveryResponse(t.Context(), &output, response); !errors.Is(err, ErrProtocol) || output.Len() != 0 {
		t.Fatal("oversized observation emitted partial frames")
	}
}

func TestRecoveryInspectionRejectsMalformedParts(t *testing.T) {
	for _, scenario := range []string{"first-index", "many-parts", "zero-total", "negative-total", "large-part", "short-part", "missing-last", "index", "total", "nonce", "mixed", "methods", "capability", "empty-part", "missing-part", "status", "unknown-field", "trailing-json", "partial-observation", "wrong-id"} {
		t.Run(scenario, func(t *testing.T) {
			serverConn, conn := net.Pipe()
			defer serverConn.Close()
			defer conn.Close()
			_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
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
				inspection := longInspectionFixture()
				if scenario == "partial-observation" {
					inspection.Files = inspection.Files[:8]
				} else if scenario == "wrong-id" {
					inspection.TransactionID = "other"
				}
				data, _ := json.Marshal(inspection)
				if scenario == "unknown-field" {
					data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
				} else if scenario == "trailing-json" {
					data = append(data, []byte(" {}")...)
				}
				total := (len(data) + inspectionPartSize - 1) / inspectionPartSize
				for index := 0; index < total; index++ {
					if scenario == "missing-last" && index == total-1 {
						return
					}
					part := &inspectionPart{Index: index, Total: total, Data: data[index*inspectionPartSize : min((index+1)*inspectionPartSize, len(data))]}
					response := Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("b", 64), InspectionPart: part}
					if index == 0 {
						switch scenario {
						case "first-index":
							part.Index = 1
						case "many-parts":
							part.Total = maxInspectionParts + 1
						case "zero-total":
							part.Total = 0
						case "negative-total":
							part.Total = -1
						case "large-part":
							part.Data = make([]byte, inspectionPartSize+1)
						case "short-part":
							part.Data = part.Data[:100]
						}
					}
					if index == 1 {
						switch scenario {
						case "index":
							part.Index = 0
						case "total":
							part.Total++
						case "nonce":
							response.Nonce = strings.Repeat("c", 64)
						case "mixed":
							response.RecoveryPage = &deployment.PendingRecoveryPage{}
						case "methods":
							response.Methods = []Method{{ID: "email"}}
						case "capability":
							response.RecoveryAvailable = true
						case "empty-part":
							part.Data = nil
						case "missing-part":
							response.InspectionPart = nil
						case "status":
							response.OK = false
						}
					}
					if writeFrame(serverConn, response) != nil {
						return
					}
				}
			}()
			client, err := Begin(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result, err := client.InspectRecovery(strings.Repeat("t", 160))
			if err == nil || result.TransactionID != "" || len(result.Files) != 0 || client.stage != -1 {
				t.Fatal("invalid fragments returned partial observation or reusable connection")
			}
			<-done
		})
	}
}

type cancelInspectionWriter struct {
	output bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelInspectionWriter) Write(data []byte) (int, error) {
	n, err := w.output.Write(data)
	w.cancel()
	return n, err
}

func TestRecoveryInspectionCancellationStopsFragments(t *testing.T) {
	inspection := longInspectionFixture()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := &cancelInspectionWriter{cancel: cancel}
	err := writeRecoveryResponse(ctx, writer, Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: strings.Repeat("b", 64), Inspection: &inspection})
	if !errors.Is(err, context.Canceled) || bytes.Count(writer.output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("cancel did not stop after first frame: %v", err)
	}
	reader := bufio.NewReaderSize(&writer.output, MaxFrameSize+1)
	var first Response
	if readFrame(reader, &first) != nil {
		t.Fatal("first frame unreadable")
	}
	if result, err := readInspectionParts(reader, first); err == nil || result != nil {
		t.Fatal("canceled fragments accepted")
	}
	if err := writeRecoveryResponse(ctx, io.Discard, Response{Inspection: &inspection}); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled operation accepted")
	}
}
