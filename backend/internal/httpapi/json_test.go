package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBindJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "valid", body: "{\"value\":\"ok\"}\n", want: false},
		{name: "unknown field", body: "{\"value\":\"ok\",\"extra\":true}", want: true},
		{name: "trailing json", body: "{\"value\":\"ok\"}{\"value\":\"again\"}", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.body))
			var payload struct {
				Value string `json:"value"`
			}
			err := bindJSON(context, &payload, 1024)
			if (err != nil) != tt.want {
				t.Fatalf("bindJSON error = %v, want error %v", err, tt.want)
			}
			if !tt.want && payload.Value != "ok" {
				t.Fatalf("valid payload was not decoded: %+v", payload)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("unexpected response status: %d", recorder.Code)
			}
		})
	}
}

func TestBindJSONHonorsBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"value":"` + string(bytes.Repeat([]byte("x"), 128)) + `"}`
	context.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	var payload struct {
		Value string `json:"value"`
	}
	err := bindJSON(context, &payload, 64)
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("bindJSON error = %v, want MaxBytesError", err)
	}
}
