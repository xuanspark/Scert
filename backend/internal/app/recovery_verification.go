package app

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

func verifyRecoveryHTTPS(
	ctx context.Context,
	validate func(context.Context, string, string, []string) (certificate.ValidationResult, error),
	server deployment.Server,
	baseline deployment.RecoveryBaseline,
	expected string,
) (certificate.ValidationResult, error) {
	var result certificate.ValidationResult
	workflow := CertificateWorkflow{
		RemoteVerifierForServer: func(ctx context.Context, target deployment.Server, endpoint string, domains []string) (certificate.ValidationResult, error) {
			var err error
			result, err = validate(ctx, endpoint, target.Address, domains)
			return result, err
		},
	}
	err := workflow.verifyRemoteDeploymentForServer(ctx, server, baseline.Endpoint, baseline.Domains, expected)
	return result, err
}
