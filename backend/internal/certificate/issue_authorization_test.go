package certificate

import (
	"context"
	"errors"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
)

func TestServiceIssueAuthorizationBeforeSideEffects(t *testing.T) {
	for _, kind := range []string{domain.ChallengeDNS01, domain.ChallengeHTTP01} {
		t.Run(kind, func(t *testing.T) {
			item := domain.Domain{ID: "domain-1", Name: "example.com", CertificateType: domain.CertificateSingle, ChallengeType: kind}
			repo := &serviceRepository{item: item}
			service := NewService(certificateTestConfig(t.TempDir()), repo, nil, nil, serviceAccounts{})
			service.SetHTTPChallenges(httpchallenge.NewStore())
			denied := errors.New("拒绝当前签发配置")
			calls := 0
			ctx := WithIssueAuthorizer(t.Context(), func(_ context.Context, actual domain.Domain) error {
				calls++
				if actual.ID != item.ID || actual.ChallengeType != kind {
					t.Fatal("授权未使用实际读取的配置")
				}
				return denied
			})
			if _, err := service.Issue(ctx, item.ID); !errors.Is(err, denied) || calls != 1 {
				t.Fatal("授权错误未传播或授权调用次数错误：", err, calls)
			}
			if repo.updateCalls != 0 {
				t.Fatal("拒绝授权仍修改证书状态")
			}
		})
	}
}
