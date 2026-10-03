package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type certificateTaskWorkflow struct {
	workflow *CertificateWorkflow
	tasks    repository.TaskRepository
	audit    repository.AuditRepository
	logError func(error)
}

func (w *certificateTaskWorkflow) Issue(ctx context.Context, id string) (certificate.IssueResult, error) {
	return w.workflow.Issue(ctx, id)
}

func (w *certificateTaskWorkflow) Renew(ctx context.Context, id string, urgent bool, policy certificate.RenewalPolicy) error {
	attempts := 1
	if urgent && policy.MaxRetries > 1 {
		attempts = policy.MaxRetries
	}
	_, err := w.run(ctx, id, "renewal_run", "certificate_issue", attempts, policy.RetryInterval, w.workflow.IssueWithReport)
	return err
}

func (w *certificateTaskWorkflow) DeployCurrent(ctx context.Context, id string) (CertificateWorkflowResult, error) {
	return w.run(ctx, id, "certificate_deploy", "certificate_deploy", 1, 0, w.workflow.DeployCurrent)
}

func (w *certificateTaskWorkflow) Import(ctx context.Context, request certificate.ImportRequest) (CertificateWorkflowResult, error) {
	return w.run(ctx, request.DomainID, "certificate_import", "certificate_import", 1, 0, func(ctx context.Context, _ string) (CertificateWorkflowResult, error) {
		return w.workflow.Import(ctx, request)
	})
}

func (w *certificateTaskWorkflow) Restore(ctx context.Context, domainID, version string) (CertificateWorkflowResult, error) {
	return w.run(ctx, domainID, "certificate_restore", "certificate_restore", 1, 0, func(ctx context.Context, id string) (CertificateWorkflowResult, error) {
		result, err := w.workflow.RestoreVersion(ctx, id, version)
		return CertificateWorkflowResult{Certificate: result}, err
	})
}

func (w *certificateTaskWorkflow) Validate(ctx context.Context, domainID string) (certificate.IssueResult, certificate.ValidationResult, error) {
	var validation certificate.ValidationResult
	result, err := w.run(ctx, domainID, "certificate_validate", "certificate_validate", 1, 0, func(ctx context.Context, id string) (CertificateWorkflowResult, error) {
		certificateResult, validationResult, err := w.workflow.ValidateCurrent(ctx, id)
		validation = validationResult
		return CertificateWorkflowResult{Certificate: certificateResult}, err
	})
	return result.Certificate, validation, err
}

func (w *certificateTaskWorkflow) run(ctx context.Context, id, taskType, stage string, attempts int, interval time.Duration, execute func(context.Context, string) (CertificateWorkflowResult, error)) (result CertificateWorkflowResult, resultErr error) {
	if w == nil || w.tasks == nil || w.audit == nil {
		return result, errors.New("证书任务记录服务未初始化")
	}
	if strings.TrimSpace(id) == "" {
		return result, errors.New("证书任务域名 ID 不能为空")
	}
	if strings.TrimSpace(taskType) == "" || strings.TrimSpace(stage) == "" {
		return result, errors.New("证书任务类型和阶段不能为空")
	}
	if attempts < 1 {
		return result, errors.New("证书任务至少执行一次")
	}
	if execute == nil {
		return result, errors.New("证书任务执行器未配置")
	}
	if active, err := w.tasks.FindActiveTaskByDomain(ctx, id, taskType); err == nil {
		return result, fmt.Errorf("%w：%s", repository.ErrTaskAlreadyRunning, active.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("检查活动证书任务失败：%w", err)
	}
	started := time.Now().UTC()
	task := repository.Task{
		ID: domain.NewID(), DomainID: id, TaskType: taskType,
		Status: "running", Stage: stage, Message: "正在执行证书工作流",
		OperatorID: repository.TaskOperatorFromContext(ctx),
		StartedAt:  &started, CreatedAt: started,
	}
	if err := w.tasks.CreateTask(ctx, task); err != nil {
		return result, err
	}
	taskCtx, taskCancel := context.WithCancel(ctx)
	if hooks, ok := repository.TaskCancellationHooksFromContext(ctx); ok {
		hooks.Register(task.ID, taskCancel)
		defer hooks.Unregister(task.ID)
	}
	defer taskCancel()
	taskCtx = certificate.WithProgressReporter(taskCtx, func(progressCtx context.Context, progressStage, progressMessage string) error {
		return w.tasks.UpdateTask(progressCtx, task.ID, "running", progressStage, progressMessage, &started, nil)
	})
	var activeAttempt string
	completionFailed := false
	defer func() {
		defer func() {
			if resultErr != nil && w.logError != nil {
				w.logError(resultErr)
			}
		}()
		// 收尾事务失败后保留中断记录，不能再用分散写入覆盖现场。
		if completionFailed {
			return
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		finished := time.Now().UTC()
		status, message := certificateTaskOutcome(result, resultErr)
		var report *deployment.Report
		if activeAttempt != "" {
			report = &result.Deployment
		}
		recordErr := w.tasks.FinishTaskAttempt(finishCtx, repository.TaskCompletion{
			TaskID: task.ID, AttemptID: activeAttempt, Status: status, Stage: stage,
			Message: message, StartedAt: &started, FinishedAt: finished,
			Report: report, AuditAction: taskType, WithoutAttempt: activeAttempt == "",
		})
		resultErr = errors.Join(resultErr, recordErr)
	}()
	if err := w.audit.CreateAuditLog(taskCtx, repository.AuditLog{
		TaskID: task.ID,
		Action: taskType, ResourceType: "domain", ResourceID: id,
		OperatorID: task.OperatorID,
		Status:     "running", Message: "开始执行证书工作流",
	}); err != nil {
		return result, err
	}
	for number := 1; number <= attempts; number++ {
		if err := taskCtx.Err(); err != nil {
			return result, err
		}
		if err := w.tasks.UpdateTask(taskCtx, task.ID, "running", stage, "正在执行证书工作流", &started, nil); err != nil {
			return result, err
		}
		attemptStarted := time.Now().UTC()
		attempt := repository.TaskAttempt{
			ID: domain.NewID(), TaskID: task.ID, AttemptNumber: number,
			Stage: stage, Status: "running", Message: "正在执行证书工作流",
			StartedAt: &attemptStarted, CreatedAt: attemptStarted,
		}
		if err := w.tasks.CreateAttempt(taskCtx, attempt); err != nil {
			return result, err
		}
		activeAttempt = attempt.ID
		issueCtx, cancel := context.WithTimeout(taskCtx, 30*time.Minute)
		issueCtx = deployment.WithTransactionContext(issueCtx, deployment.TransactionContext{
			TaskID: task.ID, AttemptID: attempt.ID, AttemptNumber: attempt.AttemptNumber,
		})
		issueCtx = certificate.WithDNSExecutionIdentity(issueCtx, domain.DNSExecutionIdentity{
			TaskID: task.ID, AttemptID: attempt.ID, AttemptNumber: attempt.AttemptNumber,
			Scheduled: taskType == "renewal_run" && task.OperatorID == "",
		})
		result, resultErr = execute(issueCtx, id)
		cancel()
		if resultErr == nil || number == attempts || result.Certificate.Saved.CertificatePath != "" || result.Deployment.Status == "partial" || result.Deployment.RecoveryStatus == "unconfirmed" || result.Deployment.RecoveryStatus == "failed" || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			return result, resultErr
		}
		// 取消只停止外部操作，结果保存仍使用独立且有限时的收尾上下文。
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		finished := time.Now().UTC()
		status, message := certificateTaskOutcome(result, resultErr)
		recordErr := w.tasks.FinishTaskAttempt(finishCtx, repository.TaskCompletion{
			TaskID: task.ID, AttemptID: attempt.ID, Status: status, Stage: stage,
			Message: message, StartedAt: &started, FinishedAt: finished, Report: &result.Deployment, Retry: true,
		})
		finishCancel()
		if recordErr != nil {
			completionFailed = true
			return result, errors.Join(resultErr, recordErr)
		}
		activeAttempt = ""
		if err := waitForManagementRetry(taskCtx, interval); err != nil {
			return result, errors.Join(resultErr, err)
		}
	}
	return result, resultErr
}

func taskDeploymentResults(report deployment.Report) []repository.TaskDeploymentResult {
	if len(report.Results) == 0 {
		return nil
	}
	items := make([]repository.TaskDeploymentResult, 0, len(report.Results))
	for index, item := range report.Results {
		item = deployment.PublicResult(item)
		items = append(items, repository.TaskDeploymentResult{
			ResultIndex: index,
			ServerID:    item.ServerID,
			Server:      item.Server,
			Status:      item.Status,
			Message:     item.Message,
			BackupID:    item.BackupID,
		})
	}
	return items
}

func certificateTaskOutcome(result CertificateWorkflowResult, err error) (string, string) {
	if result.Deployment.Status == "partial" {
		return "partial_success", "证书工作流已完成部分部署"
	}
	// 未确认或失败的恢复不能被取消意图掩盖，部分成功也必须保留真实状态。
	if errors.Is(err, context.Canceled) && result.Deployment.RecoveryStatus != "unconfirmed" && result.Deployment.RecoveryStatus != "failed" {
		return "cancelled", "证书工作流已取消"
	}
	if err != nil {
		return "failed", "证书工作流未完成，请检查受保护的服务日志"
	}
	return "success", "证书工作流执行完成"
}

var _ certificate.CertificateIssuer = (*certificateTaskWorkflow)(nil)
var _ certificate.RenewalExecutor = (*certificateTaskWorkflow)(nil)
