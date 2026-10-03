package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type localRecoveryServerRepo struct {
	repository.ServerRepository
}

func (localRecoveryServerRepo) GetServerByID(_ context.Context, id string) (deployment.Server, error) {
	return deployment.Server{ID: id}, nil
}

type testLocalRecoveryReader struct {
	lists    int
	inspects int
	hook     func()
	err      error
}

func (r *testLocalRecoveryReader) ListPendingRecoveries(context.Context, string) (deployment.PendingRecoveryPage, error) {
	r.lists++
	if r.hook != nil {
		r.hook()
	}
	return deployment.PendingRecoveryPage{Items: []deployment.PendingRecovery{{ID: "tx-1"}}}, r.err
}

func (r *testLocalRecoveryReader) InspectPending(ctx context.Context, id string, resolve func(context.Context, string) (deployment.Server, error)) (deployment.RecoveryInspection, error) {
	r.inspects++
	server, err := resolve(ctx, "server-1")
	if err != nil {
		return deployment.RecoveryInspection{}, err
	}
	if r.hook != nil {
		r.hook()
	}
	return deployment.RecoveryInspection{TransactionID: id, ServerID: server.ID}, r.err
}

func newLocalRecoverySession(t *testing.T, server *Server, reader *testLocalRecoveryReader) *localResetSession {
	t.Helper()
	server.recoveryInspection = reader
	server.servers = localRecoveryServerRepo{}
	session, err := server.localResetFactory(func(context.Context, string, string) error {
		t.Error("read-only inspection attempted to send a reset code")
		return errors.New("unexpected mail")
	})(t.Context(), localreset.Peer{Identity: "local-admin"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session.(*localResetSession)
}

func TestLocalRecoveryReadOnlyAndResetIsolation(t *testing.T) {
	server := newLocalResetTestServer(t)
	oldHash := server.config.Admin.PasswordHash
	reader := &testLocalRecoveryReader{}
	session := newLocalRecoverySession(t, server, reader)
	if !session.RecoveryAvailable() {
		t.Fatal("missing capability")
	}
	page, err := session.ListRecoveries(t.Context(), "")
	if err != nil || len(page.Items) != 1 {
		t.Fatal(err)
	}
	result, err := session.InspectRecovery(t.Context(), "tx-1")
	if err != nil || result.TransactionID != "tx-1" || result.ServerID != "server-1" {
		t.Fatalf("inspection: %+v %v", result, err)
	}
	if err := session.Select(t.Context(), "email"); err == nil {
		t.Fatal("recovery switched to password reset")
	}
	if err := session.Verify(t.Context(), "12345678"); err == nil {
		t.Fatal("recovery verified reset")
	}
	if err := session.Reset(t.Context(), "NewPass123!"); err == nil {
		t.Fatal("recovery reset password")
	}
	if reader.lists != 1 || reader.inspects != 1 {
		t.Fatal("unexpected business calls")
	}
	assertLocalResetUnchanged(t, server, oldHash)
}

func TestLocalRecoveryRejectsInvalidSession(t *testing.T) {
	for _, scenario := range []string{"expired", "cancelled", "changed-admin", "closed", "reset-stage", "missing-reader", "missing-repository"} {
		t.Run(scenario, func(t *testing.T) {
			server := newLocalResetTestServer(t)
			reader := &testLocalRecoveryReader{}
			session := newLocalRecoverySession(t, server, reader)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "expired":
				session.expires = time.Time{}
			case "cancelled":
				cancel()
			case "changed-admin":
				server.config.Admin.Email = "changed@example.test"
			case "closed":
				session.Close()
			case "reset-stage":
				session.stage = 1
			case "missing-reader":
				server.recoveryInspection = nil
			case "missing-repository":
				server.servers = nil
			}
			if _, err := session.ListRecoveries(ctx, ""); err == nil {
				t.Fatal("invalid session listed transactions")
			}
			if _, err := session.InspectRecovery(ctx, "tx-1"); err == nil {
				t.Fatal("invalid session inspected files")
			}
			if reader.lists != 0 || reader.inspects != 0 {
				t.Fatal("invalid session reached service")
			}
		})
	}
}

func TestLocalRecoveryDiscardsStaleOrFailedResults(t *testing.T) {
	for _, action := range []string{"list", "inspect"} {
		for _, failure := range []string{"changed-admin", "cancelled", "read-error"} {
			t.Run(action+"/"+failure, func(t *testing.T) {
				server := newLocalResetTestServer(t)
				reader := &testLocalRecoveryReader{}
				session := newLocalRecoverySession(t, server, reader)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				switch failure {
				case "changed-admin":
					reader.hook = func() {
						server.configMu.Lock()
						defer server.configMu.Unlock()
						server.config.Admin.Email = "changed@example.test"
					}
				case "cancelled":
					reader.hook = cancel
				case "read-error":
					reader.err = errors.New("secret-error")
				}
				if action == "list" {
					page, err := session.ListRecoveries(ctx, "")
					if err != localreset.ErrUnavailable || len(page.Items) != 0 {
						t.Fatal("stale list leaked")
					}
				} else {
					result, err := session.InspectRecovery(ctx, "tx-1")
					if err != localreset.ErrUnavailable || result.TransactionID != "" {
						t.Fatal("stale inspection leaked")
					}
				}
				if session.stage != -1 {
					t.Fatal("failed session remains reusable")
				}
			})
		}
	}
}

func TestLocalRecoveryNotRegisteredAsHTTPRoute(t *testing.T) {
	server := newLocalResetTestServer(t)
	for _, route := range server.router.Routes() {
		if strings.Contains(strings.ToLower(route.Path), "recovery") || strings.Contains(route.Handler, "InspectRecovery") || strings.Contains(route.Handler, "ListRecoveries") {
			t.Fatalf("local recovery exposed over HTTP: %+v", route)
		}
	}
}
