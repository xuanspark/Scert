package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSSHKeyAPIIsUnavailable(t *testing.T) {
	server := New(Dependencies{
		Config:     readyTestConfig(t.TempDir()),
		Repository: &stubDomainRepository{},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ssh-keys", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("SSH private key API should not be exposed, got status %d", response.Code)
	}
}
