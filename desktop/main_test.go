package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsCorePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/api", want: true},
		{path: "/api/v1/status", want: true},
		{path: "/healthz", want: true},
		{path: "/reset", want: true},
		{path: "/reset/confirm", want: true},
		{path: "/apix", want: false},
		{path: "/healthz-extra", want: false},
		{path: "/reset-password", want: false},
		{path: "/", want: false},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := isCorePath(test.path); got != test.want {
				t.Fatalf("isCorePath(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

func TestDesktopHandlerRoutesCoreRequests(t *testing.T) {
	core := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := desktopHandler(core)

	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "API GET", method: http.MethodGet, path: "/api/v1/status", want: http.StatusNoContent},
		{name: "reset GET", method: http.MethodGet, path: "/reset", want: http.StatusNoContent},
		{name: "API POST", method: http.MethodPost, path: "/api/v1/setup", want: http.StatusNoContent},
		{name: "non-API POST", method: http.MethodPost, path: "/unknown", want: http.StatusNoContent},
		{name: "asset fallback", method: http.MethodGet, path: "/apix", want: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}
