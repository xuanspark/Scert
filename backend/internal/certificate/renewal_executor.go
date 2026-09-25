package certificate

import "context"

type RenewalExecutor interface {
	Renew(context.Context, string, bool, RenewalPolicy) error
}
