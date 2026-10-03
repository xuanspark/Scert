package deployment

import (
	"context"
	"fmt"
)

// acquireOperation 覆盖整个部署、站点核验及收尾；人工执行器必须共用此入口。
// 它不替代数据库持久化占用和远端文件锁，等待可以取消，不能提前释放。
func (s *Service) acquireOperation(ctx context.Context) (func(), error) {
	if s == nil {
		return nil, ErrRecoveryDispositionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.operationOnce.Do(func() { s.operationGate = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.operationGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.operationGate
			return nil, err
		}
		return func() { <-s.operationGate }, nil
	}
}

func operationFailure(targets []Target, err error) Report {
	report := Report{Total: len(targets), Failed: len(targets), Status: "failed", Results: make([]Result, 0, len(targets))}
	for _, target := range targets {
		report.Results = append(report.Results, Result{
			ServerID: target.Server.ID, Server: target.Server.Name, Status: "failed",
			FailureCode: FailureUnavailable, Message: fmt.Sprintf("未获得部署操作许可：%v", err),
		})
	}
	return report
}

type recoveryClaimReader interface {
	HasRecoveryClaim(context.Context, string) (bool, error)
}

func (s *Service) checkRecoveryClaim(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, ok := s.journal.(recoveryClaimReader)
	if !ok {
		return nil
	}
	claimed, err := reader.HasRecoveryClaim(ctx, id)
	if err != nil {
		return ErrRecoveryDispositionUnavailable
	}
	if claimed {
		return ErrRecoveryDispositionBusy
	}
	return ctx.Err()
}

// checkDeploymentClaims 在连接远端前拦截仍有人工占用的服务器。
// 同地址同端口的别名也须检查；凭据或路径修改不能绕过原服务器占用。
func (s *Service) checkDeploymentClaims(ctx context.Context, server Server) error {
	if _, ok := s.journal.(recoveryClaimReader); !ok {
		return nil
	}
	records, err := s.journal.ListPendingDeploymentTransactions(ctx)
	if err != nil {
		return ErrRecoveryDispositionUnavailable
	}
	for _, record := range records {
		sameServer := record.ServerID == server.ID
		if record.Evidence != nil {
			sameServer = sameServer || (record.Evidence.Address == server.Address && record.Evidence.Port == server.Port)
		}
		if sameServer {
			if err := s.checkRecoveryClaim(ctx, record.ID); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
