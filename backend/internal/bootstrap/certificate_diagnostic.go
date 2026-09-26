package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/go-acme/lego/v5/acme"
)

// certificateFailureDiagnostic 只输出受控分类和数值，禁止输出远端说明、地址及凭据。
func certificateFailureDiagnostic(err error) string {
	category := "unclassified"
	status := 0
	retrySeconds := int64(0)
	var problem *acme.ProblemDetails
	var rateLimit *acme.RateLimitedError
	var network *net.OpError
	switch {
	case errors.As(err, &problem):
		category = "acme_other"
		if problem.HTTPStatus >= 100 && problem.HTTPStatus <= 599 {
			status = problem.HTTPStatus
		}
		switch problem.Type {
		case acme.RateLimitedErrorType:
			category = "acme_rate_limited"
		case acme.DNSErrorType:
			category = "acme_dns"
		case acme.ConnectionErrorType:
			category = "acme_connection"
		case acme.AccountDoesNotExistErrorType:
			category = "acme_account_missing"
		case acme.UnauthorizedErrorType:
			category = "acme_unauthorized"
		case acme.CaaErrorType:
			category = "acme_caa"
		case acme.InvalidContactErrorType:
			category = "acme_invalid_contact"
		case acme.RejectedIdentifierErrorType:
			category = "acme_rejected_identifier"
		}
		if errors.As(err, &rateLimit) && rateLimit.RetryAfter > 0 {
			retrySeconds = int64(rateLimit.RetryAfter.Seconds())
		}
	case errors.Is(err, context.DeadlineExceeded):
		category = "deadline"
	case errors.Is(err, context.Canceled):
		category = "canceled"
	case errors.Is(err, os.ErrPermission):
		category = "filesystem_permission"
	case errors.Is(err, os.ErrNotExist):
		category = "filesystem_missing"
	case errors.As(err, &network):
		category = "network"
	}
	return fmt.Sprintf("管理证书签发失败：stage=management_certificate category=%s acme_status=%d retry_after_seconds=%d", category, status, retrySeconds)
}
