package deployment

import (
	"context"
	"errors"
	"fmt"
)

// Verification 将站点生效校验与恢复核验置于远端文件事务之内。
type Verification struct {
	Verify         func(context.Context) error
	VerifyRecovery func(context.Context) error
}

type pendingDeployment struct {
	index   int
	commit  func(context.Context) Result
	restore func(context.Context) Result
	finish  func(bool) Result
}

func (s *Service) DeploySavedVerified(ctx context.Context, targets []Target, saved SavedFiles, verification Verification) (Report, error) {
	artifact, err := loadArtifact(saved)
	if err != nil {
		return Report{Status: "failed"}, err
	}
	return s.DeployVerified(ctx, targets, artifact, verification)
}

func (s *Service) DeployVerified(ctx context.Context, targets []Target, artifact Artifact, verification Verification) (Report, error) {
	if verification.Verify == nil {
		return Report{Status: "failed"}, errors.New("缺少部署后站点校验，未执行推送")
	}
	pending := make([]*pendingDeployment, 0, len(targets))
	// 异常退出时关闭连接但保留现场；不得以释放锁掩盖未确认的事务。
	defer func() {
		for _, transaction := range pending {
			transaction.finish(true)
		}
	}()
	report := s.deploy(ctx, targets, artifact, &pending)
	var deployErr error
	if report.Status != "success" {
		deployErr = fmt.Errorf("证书部署未全部成功：成功 %d 台，失败 %d 台", report.Success, report.Failed)
	} else {
		report.VerificationStatus = "failed"
		deployErr = ctx.Err()
		if deployErr == nil {
			deployErr = verification.Verify(ctx)
		}
		if deployErr == nil {
			deployErr = ctx.Err()
		}
		if deployErr != nil {
			deployErr = fmt.Errorf("部署后 HTTPS 校验失败：%w", deployErr)
		} else {
			report.VerificationStatus = "success"
		}
	}
	if deployErr != nil {
		if len(pending) > 0 {
			s.restorePending(ctx, &report, pending, verification)
		}
		summarizeReport(&report)
		report.Status = "failed"
		return report, deployErr
	}
	for _, transaction := range pending {
		result := transaction.commit(ctx)
		report.Results[transaction.index] = transaction.finish(result.Status != "success")
	}
	summarizeReport(&report)
	if report.Status != "success" {
		return report, errors.New("站点校验通过，但事务清理或释放部署锁未完成")
	}
	return report, nil
}

func (s *Service) restorePending(ctx context.Context, report *Report, pending []*pendingDeployment, verification Verification) {
	report.RecoveryStatus = "unconfirmed"
	for index := len(pending) - 1; index >= 0; index-- {
		transaction := pending[index]
		// 请求取消后仍使用独立有界上下文恢复，不重放签发或文件推送。
		recoveryContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
		report.Results[transaction.index] = transaction.restore(recoveryContext)
		cancel()
	}
	restored := true
	for _, result := range report.Results {
		switch result.FailureCode {
		case FailureRecoveryPending, FailureConfiguration, FailureConnection, FailureDirectory, FailureUpload, FailureReplace, FailureValidation:
			// 这些失败要么未触碰目标，要么已完成文件回滚且未执行重载。
		default:
			restored = false
		}
	}
	var recoveryErr error
	if !restored {
		report.RecoveryStatus = "failed"
	} else if verification.VerifyRecovery != nil {
		recoveryContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentRollbackTimeout)
		err := verification.VerifyRecovery(recoveryContext)
		if err == nil {
			err = recoveryContext.Err()
		}
		cancel()
		if err == nil {
			report.RecoveryStatus = "success"
		} else {
			recoveryErr = err
		}
	}
	for _, transaction := range pending {
		result := transaction.finish(report.RecoveryStatus != "success")
		if result.FailureCode == FailureLockRelease {
			report.RecoveryStatus = "failed"
		}
		if result.FailureCode == FailureRecoveryPending {
			if report.RecoveryStatus == "success" {
				result.FailureCode = FailureRecoveryRestored
			} else {
				result.FailureCode = FailureRecoveryUnknown
			}
			result.Message = publicFailureMessages[result.FailureCode]
			if recoveryErr != nil {
				result.Message += fmt.Sprintf("；站点恢复核验失败：%v", recoveryErr)
			}
		}
		report.Results[transaction.index] = result
	}
}

func summarizeReport(report *Report) {
	report.Success, report.Failed = 0, 0
	for _, result := range report.Results {
		if result.Status == "success" {
			report.Success++
		} else {
			report.Failed++
		}
	}
	switch {
	case report.Total == 0 || report.Success == 0:
		report.Status = "failed"
	case report.Failed == 0:
		report.Status = "success"
	default:
		report.Status = "partial"
	}
}
