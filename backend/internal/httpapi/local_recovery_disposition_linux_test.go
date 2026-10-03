//go:build linux

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
)

func TestLocalDispositionUnixIdentityAndLostResponse(t *testing.T) {
	for _, mode := range []string{"normal", "lost-response", "foreign-session"} {
		t.Run(mode, func(t *testing.T) {
			server, _, journal := localDispositionFixture(t)
			path := filepath.Join(t.TempDir(), "control.sock")
			listener, err := localreset.Listen(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("socket is not protected")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			done := make(chan error, 1)
			go func() {
				done <- listener.Serve(ctx, server.localResetFactory(func(context.Context, string, string) error { return nil }))
			}()
			defer func() {
				cancel()
				_ = listener.Close()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			conn, err := localreset.Dial(ctx, path, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			reader, writer := json.NewDecoder(conn), json.NewEncoder(conn)
			var challenge localreset.Response
			if err := reader.Decode(&challenge); err != nil || !challenge.DispositionAvailable {
				t.Fatalf("challenge: %v", err)
			}
			if err := writer.Encode(localreset.Request{Version: localreset.Version, Nonce: challenge.Nonce, Action: "recovery_prepare", TransactionID: "tx-1", RecoveryAction: deployment.RecoveryActionRecord}); err != nil {
				t.Fatal(err)
			}
			var preparation localreset.Response
			if err := reader.Decode(&preparation); err != nil || preparation.Preparation == nil {
				t.Fatalf("prepare: %+v %v", preparation, err)
			}
			prepared := preparation.Preparation
			if mode == "foreign-session" {
				_ = conn.Close()
				other, err := localreset.Dial(ctx, path, uint32(os.Geteuid()))
				if err != nil {
					t.Fatal(err)
				}
				client, err := localreset.Begin(ctx, other)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if _, err := client.ExecuteRecovery(prepared.DispositionID, prepared.Token); err == nil {
					t.Fatal("confirmation escaped original session")
				}
				journal.mu.Lock()
				defer journal.mu.Unlock()
				if journal.begins != 0 {
					t.Fatal("foreign session wrote intent")
				}
				return
			}
			if err := writer.Encode(localreset.Request{Version: localreset.Version, Nonce: preparation.Nonce, Action: "recovery_execute", DispositionID: prepared.DispositionID, ConfirmationToken: prepared.Token}); err != nil {
				t.Fatal(err)
			}
			if mode == "normal" {
				var result localreset.Response
				if err := reader.Decode(&result); err != nil || result.Disposition == nil || result.Disposition.ResultCode != "observed" {
					t.Fatalf("execute: %+v %v", result, err)
				}
			}
			_ = conn.Close()
			for {
				journal.mu.Lock()
				finished := journal.finishes == 1
				journal.mu.Unlock()
				if finished {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("execution never reached durable result")
				case <-time.After(5 * time.Millisecond):
				}
			}
			other, err := localreset.Dial(ctx, path, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			client, err := localreset.Begin(ctx, other)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result, err := client.GetRecoveryDisposition(prepared.DispositionID)
			if err != nil || result.Status != "succeeded" || result.ResultCode != "observed" {
				t.Fatalf("reconnect query: %+v %v", result, err)
			}
			journal.mu.Lock()
			defer journal.mu.Unlock()
			if journal.begins != 1 || journal.finishes != 1 || journal.queries != 1 || journal.item.Intent.Operator != fmt.Sprintf("uid:%d", os.Geteuid()) {
				t.Fatal("identity not from socket or query replayed")
			}
		})
	}
}
