package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerReturnsServiceUnavailableWhenRuntimeIsNil(t *testing.T) {
	var runtime *Runtime
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("空运行时状态码错误：%d", response.Code)
	}
}
