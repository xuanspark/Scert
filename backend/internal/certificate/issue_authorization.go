package certificate

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type issueAuthorizerContextKey struct{}

// IssueAuthorizer 检查签发实际使用的配置快照，而不是先前读取的验证方式。
type IssueAuthorizer func(context.Context, domain.Domain) error

// WithIssueAuthorizer 为用户发起的工作流绑定实时授权；系统续期不使用用户会话。
func WithIssueAuthorizer(ctx context.Context, authorize IssueAuthorizer) context.Context {
	return context.WithValue(ctx, issueAuthorizerContextKey{}, authorize)
}

func authorizeIssueSnapshot(ctx context.Context, item domain.Domain) error {
	authorize, _ := ctx.Value(issueAuthorizerContextKey{}).(IssueAuthorizer)
	if authorize == nil {
		return nil
	}
	return authorize(ctx, item)
}
