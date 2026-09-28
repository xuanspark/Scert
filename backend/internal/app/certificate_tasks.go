package app

import (
	"context"
	"errors"
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

func (w *certificateTaskWorkflow) run(ctx context.Context, id, taskType, stage string, attempts int, interval time.Duration, execute func(context.Context, string) (CertificateWorkflowResult, error)) (result CertificateWorkflowResult, resultErr error) {
	if w.tasks == nil || w.audit == nil {
		return result, errors.New("证书任务记录服务未初始化")
	}
	started := time.Now().UTC()
	task := repository.Task{
		ID: domain.NewID(), DomainID: id, TaskType: taskType,
		Status: "running", Stage: stage, Message: "正在执行证书工作流",
		StartedAt: &started, CreatedAt: started,
	}
	if err := w.tasks.CreateTask(ctx, task); err != nil {
		return result, err
	}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		finished := time.Now().UTC()
		status, message := certificateTaskOutcome(resultErr)
		auditErr := w.audit.CreateAuditLog(finishCtx, repository.AuditLog{
			TaskID: task.ID,
			Action: taskType, ResourceType: "domain", ResourceID: id,
			Status: status, Message: message,
		})
		if auditErr != nil {
			status, message = certificateTaskOutcome(auditErr)
			stage = "record"
		}
		taskErr := w.tasks.UpdateTask(finishCtx, task.ID, status, stage, message, &started, &finished)
		resultErr = errors.Join(resultErr, taskErr, auditErr)
		if resultErr != nil && w.logError != nil {
			w.logError(resultErr)
		}
	}()
	if err := w.audit.CreateAuditLog(ctx, repository.AuditLog{
		TaskID: task.ID,
		Action: taskType, ResourceType: "domain", ResourceID: id,
		Status: "running", Message: "开始执行证书工作流",
	}); err != nil {
		return result, err
	}
	for number := 1; number <= attempts; number++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := w.tasks.UpdateTask(ctx, task.ID, "running", stage, "正在执行证书工作流", &started, nil); err != nil {
			return result, err
		}
		attemptStarted := time.Now().UTC()
		attempt := repository.TaskAttempt{
			ID: domain.NewID(), TaskID: task.ID, AttemptNumber: number,
			Stage: stage, Status: "running", Message: "正在执行证书工作流",
			StartedAt: &attemptStarted, CreatedAt: attemptStarted,
		}
		if err := w.tasks.CreateAttempt(ctx, attempt); err != nil {
			return result, err
		}
		issueCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		result, resultErr = execute(issueCtx, id)
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		finished := time.Now().UTC()
		status, message := certificateTaskOutcome(resultErr)
		recordErr := w.tasks.FinishAttempt(finishCtx, attempt.ID, status, message, &finished)
		finishCancel()
		if recordErr != nil {
			return result, errors.Join(resultErr, recordErr)
		}
		if resultErr == nil || number == attempts || result.Certificate.Saved.CertificatePath != "" || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			return result, resultErr
		}
		if err := w.tasks.UpdateTask(ctx, task.ID, "running", "retry_wait", "证书申请失败，等待重试", &started, nil); err != nil {
			return result, errors.Join(resultErr, err)
		}
		if err := waitForManagementRetry(ctx, interval); err != nil {
			return result, errors.Join(resultErr, err)
		}
	}
	return result, resultErr
}

func certificateTaskOutcome(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return "cancelled", "证书工作流已取消"
	}
	if err != nil {
		return "failed", "证书工作流未完成，请检查受保护的服务日志"
	}
	return "success", "证书工作流执行完成"
}

var _ certificate.CertificateIssuer = (*certificateTaskWorkflow)(nil)
var _ certificate.RenewalExecutor = (*certificateTaskWorkflow)(nil)
