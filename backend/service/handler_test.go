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

func TestHandlerReturnsServiceUnavailableForZeroRuntime(t *testing.T) {
	runtime := &Runtime{}
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("未打开的运行时应返回服务不可用：%d", response.Code)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}
