package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
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
	if w.tasks == nil || w.audit == nil {
		return result, errors.New("证书任务记录服务未初始化")
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
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		finished := time.Now().UTC()
		status, message := certificateTaskOutcome(result, resultErr)
		auditErr := w.audit.CreateAuditLog(finishCtx, repository.AuditLog{
			TaskID: task.ID,
			Action: taskType, ResourceType: "domain", ResourceID: id,
			OperatorID: task.OperatorID,
			Status:     status, Message: message,
		})
		if auditErr != nil {
			status, message = certificateTaskOutcome(result, auditErr)
			stage = "record"
		}
		taskErr := w.tasks.UpdateTask(finishCtx, task.ID, status, stage, message, &started, &finished)
		resultErr = errors.Join(resultErr, taskErr, auditErr)
		if resultErr != nil && w.logError != nil {
			w.logError(resultErr)
		}
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
		issueCtx, cancel := context.WithTimeout(taskCtx, 30*time.Minute)
		result, resultErr = execute(issueCtx, id)
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		finished := time.Now().UTC()
		status, message := certificateTaskOutcome(result, resultErr)
		recordErr := w.tasks.FinishAttempt(finishCtx, attempt.ID, status, message, &finished)
		finishCancel()
		if recordErr != nil {
			return result, errors.Join(resultErr, recordErr)
		}
		if resultErr == nil || number == attempts || result.Certificate.Saved.CertificatePath != "" || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			return result, resultErr
		}
		if err := w.tasks.UpdateTask(taskCtx, task.ID, "retry_wait", "retry_wait", "证书申请失败，等待重试", &started, nil); err != nil {
			return result, errors.Join(resultErr, err)
		}
		if err := waitForManagementRetry(taskCtx, interval); err != nil {
			return result, errors.Join(resultErr, err)
		}
	}
	return result, resultErr
}

func certificateTaskOutcome(result CertificateWorkflowResult, err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return "cancelled", "\u8bc1\u4e66\u5de5\u4f5c\u6d41\u5df2\u53d6\u6d88"
	}
	if result.Deployment.Status == "partial" {
		return "partial_success", "\u8bc1\u4e66\u5de5\u4f5c\u6d41\u5df2\u5b8c\u6210\u90e8\u5206\u90e8\u7f72"
	}
	if err != nil {
		return "failed", "\u8bc1\u4e66\u5de5\u4f5c\u6d41\u672a\u5b8c\u6210\uff0c\u8bf7\u68c0\u67e5\u53d7\u4fdd\u62a4\u7684\u670d\u52a1\u65e5\u5fd7"
	}
	return "success", "\u8bc1\u4e66\u5de5\u4f5c\u6d41\u6267\u884c\u5b8c\u6210"
}

var _ certificate.CertificateIssuer = (*certificateTaskWorkflow)(nil)
var _ certificate.RenewalExecutor = (*certificateTaskWorkflow)(nil)
