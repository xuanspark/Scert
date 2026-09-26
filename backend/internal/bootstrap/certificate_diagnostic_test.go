package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/acme"
)

func TestCertificateDiagnosticRedactsUntrustedFields(t *testing.T) {
	for _, item := range []struct {
		name     string
		err      error
		category string
		status   int
		retry    int
	}{
		{"rate", &acme.RateLimitedError{ProblemDetails: &acme.ProblemDetails{
			Type: acme.RateLimitedErrorType, HTTPStatus: 429, Detail: "secret-value",
			URL: "https://secret-value/", Instance: "secret-value", Method: "secret-value",
			SubProblems: []acme.SubProblem{{Type: "secret-value", Detail: "secret-value"}},
		}, RetryAfter: 2 * time.Hour}, "acme_rate_limited", 429, 7200},
		{"unknown", &acme.ProblemDetails{Type: "secret-value", Detail: "secret-value", HTTPStatus: -1}, "acme_other", 0, 0},
		{"dns", &acme.ProblemDetails{Type: acme.DNSErrorType, HTTPStatus: 400}, "acme_dns", 400, 0},
		{"permission", fmt.Errorf("secret-value: %w", os.ErrPermission), "filesystem_permission", 0, 0},
		{"timeout", context.DeadlineExceeded, "deadline", 0, 0},
		{"opaque", errors.New("secret-value"), "unclassified", 0, 0},
	} {
		t.Run(item.name, func(t *testing.T) {
			message := certificateFailureDiagnostic(fmt.Errorf("secret-value: %w", item.err))
			want := fmt.Sprintf("category=%s acme_status=%d retry_after_seconds=%d", item.category, item.status, item.retry)
			if !strings.Contains(message, want) || strings.Contains(message, "secret-value") {
				t.Fatalf("unsafe or incomplete diagnostic: %s", message)
			}
		})
	}
}

func TestCompleterReportsSafeCertificateFailure(t *testing.T) {
	fixture := newCompletionFixture(t, "admin.example.test")
	fixture.issuer.err = &acme.ProblemDetails{Type: acme.RateLimitedErrorType, HTTPStatus: 429, Detail: "secret-value"}
	var warnings []string
	completer := fixture.completer(func(dependencies *CompleteDependencies) {
		dependencies.Warn = func(message string) { warnings = append(warnings, message) }
	})
	_, err := completer.Complete(context.Background(), fixture.request("admin.example.test"))
	if err == nil || len(warnings) != 1 || !strings.Contains(warnings[0], "acme_rate_limited") || strings.Contains(warnings[0], "secret-value") {
		t.Fatalf("failure not safely reported: err=%v warnings=%v", err, warnings)
	}
	if _, err := os.Stat(fixture.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed certificate issuance must not commit configuration")
	}
}
