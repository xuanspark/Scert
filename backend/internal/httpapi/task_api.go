package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) issueDomainAsTask(c *gin.Context) {
	s.submitDomainTask(c, "certificate_issue")
}

func (s *Server) submitDomainTask(c *gin.Context, taskType string) {
	s.submitPreparedDomainTask(c, taskType, nil)
}

type preparedCertificateTask struct {
	operation func(context.Context, string) (certificate.IssueResult, deployment.Report, error)
	cleanup   func()
}

func (s *Server) submitPreparedDomainTask(c *gin.Context, taskType string, prepared *preparedCertificateTask) {
	transferred := false
	defer func() {
		if !transferred && prepared != nil && prepared.cleanup != nil {
			prepared.cleanup()
		}
	}()
	if (taskType == "certificate_issue" && s.issueCertificate == nil) ||
		(taskType == "certificate_deploy" && s.deployCertificateTask == nil) ||
		(taskType == "certificate_validate" && s.validateCertificateTask == nil) ||
		(taskType == "certificate_restore" && s.restoreVersionTask == nil) {
		s.writeError(c, http.StatusNotImplemented, "证书申请接口尚未配置", errors.New("证书申请工作流未配置"))
		return
	}
	if taskRepositoryUnavailable(s.tasks) {
		s.writeError(c, http.StatusNotImplemented, "证书任务接口尚未配置", errors.New("任务仓储未配置"))
		return
	}
	if s.projectResources == nil || s.projectMembers == nil {
		s.writeError(c, http.StatusServiceUnavailable, "证书任务权限服务尚未就绪", errors.New("项目资源或项目权限服务未配置"))
		return
	}
	domainID := strings.TrimSpace(c.Param("id"))
	resource, err := s.projectResources.GetVisibleDomain(c.Request.Context(), c.GetString(authenticatedActorKey), domainID)
	if err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) || errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的证书任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取域名失败", err)
		return
	}
	authorizer, ok := s.projectMembers.(repository.ProjectAuthorizer)
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "证书任务权限服务尚未就绪", errors.New("项目授权服务未配置"))
		return
	}
	if taskType == "certificate_issue" || taskType == "certificate_import" {
		if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), resource.ResourceScope.Normalize().ProjectID, domain.PermissionCertificateManage); err != nil {
			if errors.Is(err, repository.ErrProjectAccessDenied) {
				s.writeError(c, http.StatusForbidden, "无权管理此项目的证书任务", nil)
				return
			}
			s.writeError(c, http.StatusInternalServerError, "检查证书任务权限失败", err)
			return
		}
	}
	permission := domain.PermissionDeploymentExecute
	if taskType == "certificate_validate" {
		permission = domain.PermissionPrivateKeyRead
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), resource.ResourceScope.Normalize().ProjectID, permission); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			message := "无权执行此项目的证书部署任务"
			if taskType == "certificate_validate" {
				message = "无权读取此项目的私钥进行证书校验"
			}
			s.writeError(c, http.StatusForbidden, message, nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "检查证书部署权限失败", err)
		return
	}
	if taskType == "certificate_issue" && !s.authorizeIssueChallenge(c, resource) {
		return
	}
	s.issueTaskMu.Lock()
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey != "" {
		if !validTaskIdempotencyKey(idempotencyKey) {
			s.issueTaskMu.Unlock()
			s.writeError(c, http.StatusBadRequest, "幂等键格式无效", errors.New("幂等键长度或字符无效"))
			return
		}
		if existing, err := s.tasks.FindTaskByIdempotencyKey(c.Request.Context(), domainID, idempotencyKey); err == nil {
			if existing.TaskType != taskType {
				s.issueTaskMu.Unlock()
				s.writeError(c, http.StatusConflict, "幂等键已用于另一种操作", nil)
				return
			}
			status := http.StatusOK
			if isActiveTaskStatus(existing.Status) {
				status = http.StatusConflict
			}
			c.JSON(status, gin.H{"task": existing, "replayed": true})
			s.issueTaskMu.Unlock()
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			s.issueTaskMu.Unlock()
			s.writeError(c, http.StatusInternalServerError, "检查幂等任务失败", err)
			return
		}
	}
	if active, err := s.tasks.FindActiveTaskByDomain(c.Request.Context(), domainID, taskType); err == nil {
		s.issueTaskMu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "该域名已有正在执行的证书任务", "task": active})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.issueTaskMu.Unlock()
		s.writeError(c, http.StatusInternalServerError, "检查正在执行的任务失败", err)
		return
	}
	now := time.Now().UTC()
	task := repository.Task{
		HistoryScope: repository.HistoryScope{ScopeType: repository.ScopeProject, ProjectID: resource.ResourceScope.Normalize().ProjectID},
		OperatorID:   s.currentOperatorID(c),
		ID:           domain.NewID(), DomainID: domainID, TaskType: taskType, Status: "queued", Stage: "queued",
		Message: "等待执行证书任务", IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.tasks.CreateTask(c.Request.Context(), task); err != nil {
		if idempotencyKey != "" {
			if existing, lookupErr := s.tasks.FindTaskByIdempotencyKey(c.Request.Context(), domainID, idempotencyKey); lookupErr == nil {
				if existing.TaskType != taskType {
					s.issueTaskMu.Unlock()
					s.writeError(c, http.StatusConflict, "幂等键已用于另一种操作", nil)
					return
				}
				status := http.StatusOK
				if isActiveTaskStatus(existing.Status) {
					status = http.StatusConflict
				}
				c.JSON(status, gin.H{"task": existing, "replayed": true})
				s.issueTaskMu.Unlock()
				return
			}
		}
		s.issueTaskMu.Unlock()
		if errors.Is(err, repository.ErrTaskAlreadyRunning) {
			c.JSON(http.StatusConflict, gin.H{"error": "该域名已有正在执行的同类证书任务"})
			return
		}
		s.writeError(c, http.StatusInternalServerError, "创建证书任务失败", err)
		return
	}
	s.issueTaskMu.Unlock()
	attempt := repository.TaskAttempt{
		ID: domain.NewID(), TaskID: task.ID, AttemptNumber: 1, Stage: taskType, Status: "running",
		Message: "等待执行证书任务", StartedAt: &now, CreatedAt: now,
	}
	if err := s.tasks.CreateAttempt(c.Request.Context(), attempt); err != nil {
		_ = s.tasks.UpdateTask(context.Background(), task.ID, "failed", taskType, "创建任务尝试失败", &now, &now)
		s.writeError(c, http.StatusInternalServerError, "创建任务尝试失败", err)
		return
	}
	if prepared != nil {
		transferred = s.enqueueCertificateTask(c, task, attempt, taskType, prepared.operation,
			taskType, "正在导入证书并部署", "证书导入和部署完成", prepared.cleanup)
		if transferred {
			writeAcceptedCertificateTask(c, task)
		}
		return
	}
	if taskType == "certificate_deploy" {
		s.executeCertificateDeployTask(c, task, attempt, taskType)
		return
	}
	if taskType == "certificate_validate" {
		s.executeCertificateValidationTask(c, task, attempt)
		return
	}
	s.executeCertificateIssueTask(c, task, attempt, taskType)
}

func (s *Server) executeCertificateIssueTask(c *gin.Context, task repository.Task, attempt repository.TaskAttempt, auditAction string) {
	if s.enqueueCertificateTask(c, task, attempt, auditAction, s.issueCertificate, "certificate_issue", "正在申请证书并部署", "证书申请和部署完成", nil) {
		writeAcceptedCertificateTask(c, task)
	}
}

func (s *Server) executeCertificateDeployTask(c *gin.Context, task repository.Task, attempt repository.TaskAttempt, auditAction string) {
	if s.enqueueCertificateTask(c, task, attempt, auditAction, s.deployCertificateTask, "certificate_deploy", "正在部署证书", "证书部署完成", nil) {
		writeAcceptedCertificateTask(c, task)
	}
}

func (s *Server) runCertificateTask(ctx context.Context, task repository.Task, attempt repository.TaskAttempt, auditAction string, operation func(context.Context, string) (certificate.IssueResult, deployment.Report, error), stage, runningMessage, successMessage string) {
	s.runCertificateOperation(ctx, task, attempt, auditAction, func(ctx context.Context, id string) (certificateTaskResult, error) {
		result, report, err := operation(ctx, id)
		return certificateTaskResult{Certificate: result, Deployment: &report}, err
	}, stage, runningMessage, successMessage)
}

type certificateTaskResult struct {
	Certificate certificate.IssueResult
	Deployment  *deployment.Report
	Validation  *certificate.ValidationResult
}

func (s *Server) runCertificateOperation(ctx context.Context, task repository.Task, attempt repository.TaskAttempt, auditAction string, operation func(context.Context, string) (certificateTaskResult, error), stage, runningMessage, successMessage string) {
	if task.TaskType == "certificate_restore" {
		runningMessage = "\u6b63\u5728\u6062\u590d\u672c\u5730\u8bc1\u4e66\u5386\u53f2\u7248\u672c"
		successMessage = "\u672c\u5730\u8bc1\u4e66\u5386\u53f2\u7248\u672c\u6062\u590d\u5b8c\u6210\uff0c\u5c1a\u672a\u4ee3\u8868\u8fdc\u7aef\u670d\u52a1\u5668\u90e8\u7f72\u5b8c\u6210"
	}
	executionCtx := deployment.WithTransactionContext(ctx, deployment.TransactionContext{
		TaskID: task.ID, AttemptID: attempt.ID, AttemptNumber: attempt.AttemptNumber,
	})
	now := time.Now().UTC()
	if err := executionCtx.Err(); err != nil {
		s.finishTaskContext(task, attempt, err)
		return
	}
	if err := s.tasks.UpdateTask(executionCtx, task.ID, "running", stage, runningMessage, &now, nil); err != nil {
		failureMessage := "任务启动失败"
		if s.finishCancelledTask(task, attempt, executionCtx) {
			return
		}
		attempt.Stage = stage
		s.finishTaskBeforeExecution(task, attempt, "failed", failureMessage)
		return
	}
	if s.audit != nil {
		if err := s.audit.CreateAuditLog(executionCtx, repository.AuditLog{
			TaskID: task.ID, OperatorID: task.OperatorID,
			Action: auditAction, ResourceType: "domain", ResourceID: task.DomainID,
			Status: "running", Message: runningMessage,
		}); err != nil {
			failureMessage := "任务启动审计记录失败"
			if s.finishCancelledTask(task, attempt, executionCtx) {
				return
			}
			attempt.Stage = "record"
			s.finishTaskBeforeExecution(task, attempt, "failed", failureMessage)
			return
		}
	}
	if s.finishCancelledTask(task, attempt, executionCtx) {
		return
	}
	executionCtx = certificate.WithProgressReporter(executionCtx, func(progressCtx context.Context, progressStage, progressMessage string) error {
		return s.tasks.UpdateTask(progressCtx, task.ID, "running", progressStage, progressMessage, &now, nil)
	})
	if err := s.authorizeCertificateTaskExecution(executionCtx, task, attempt); err != nil {
		if s.finishCancelledTask(task, attempt, executionCtx) {
			return
		}
		attempt.Stage = "authorize"
		s.finishTaskBeforeExecution(task, attempt, "failed", "任务执行前权限或资源归属校验未通过，未执行证书操作")
		return
	}
	if s.finishCancelledTask(task, attempt, executionCtx) {
		return
	}
	if task.TaskType == "certificate_issue" {
		executionCtx = s.withCertificateIssueAuthorization(executionCtx, task.OperatorID, task.DomainID, task.ProjectID, attempt.AttemptNumber > 1)
	}
	result, issueErr := operation(executionCtx, task.DomainID)
	certificateResult := result.Certificate
	var deploymentReport deployment.Report
	if result.Deployment != nil {
		deploymentReport = *result.Deployment
	}
	if task.TaskType == "certificate_import" {
		deploymentReport.CertificateImported = issueErr == nil || (certificateResult.DomainID == task.DomainID &&
			certificateResult.Saved.CertificatePath != "" && certificateResult.Saved.FullChainPath != "" && certificateResult.Saved.PrivateKeyPath != "")
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	deploymentReport = sanitizeDeploymentReport(deploymentReport)
	finished := time.Now().UTC()
	status := "success"
	message := successMessage
	partial := deploymentReport.Status == "partial"
	if issueErr != nil {
		status = "failed"
		message = certificateTaskErrorMessage(issueErr)
		if partial {
			status = "partial_success"
			message = "\u8bc1\u4e66\u5df2\u7b7e\u53d1\uff0c\u4f46\u4ec5\u90e8\u5206\u670d\u52a1\u5668\u90e8\u7f72\u6210\u529f"
		}
		if s.logger != nil {
			s.logger.Error("证书任务执行失败", "task_id", task.ID, "domain_id", task.DomainID, "error", issueErr)
		}
		if errors.Is(issueErr, context.Canceled) && !partial && deploymentReport.RecoveryStatus != "unconfirmed" && deploymentReport.RecoveryStatus != "failed" {
			status = "cancelled"
		}
	} else if partial {
		status = "partial_success"
	}
	if task.TaskType == "certificate_import" && issueErr != nil {
		message = "证书导入失败"
		if deploymentReport.CertificateImported {
			message = "证书已导入，但部署未完成。请检查部署结果后重试部署，无需重新导入。"
		}
	}
	if task.TaskType == "certificate_validate" && issueErr != nil {
		message = "证书校验失败，请检查当前证书、私钥匹配及证书链"
		if status == "cancelled" {
			message = "证书校验已取消"
		}
		if errors.Is(issueErr, context.DeadlineExceeded) {
			message = "证书校验超时"
		}
	}
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if persisted, err := s.tasks.GetTask(finishCtx, task.ID); err != nil {
		status, message = "failed", "无法确认任务收尾状态，请核查部署结果后再重试"
		issueErr = errors.Join(issueErr, err)
	} else if persisted.Status == "cancelled" || persisted.Status == "cancelling" {
		// 取消请求只是意图，不能覆盖已经完成的外部操作，也不能隐藏失败及部分成功。
		if issueErr == nil && !partial {
			message = "任务已完成，取消请求未撤销已完成的操作"
		} else if status == "cancelled" {
			status = "cancelled"
			message = "任务已停止；已执行操作是否回滚请查看部署结果"
			if task.TaskType == "certificate_validate" {
				message = "证书校验已停止，未保存通过结果"
			}
		}
	}
	completion := repository.TaskCompletion{
		TaskID: task.ID, AttemptID: attempt.ID, Status: status, Stage: stage,
		Message: message, StartedAt: &now, FinishedAt: finished,
		AuditAction: auditAction,
	}
	if result.Deployment != nil {
		completion.Report = &deploymentReport
	}
	if status == "success" {
		completion.Validation = result.Validation
	}
	if err := s.tasks.FinishTaskAttempt(finishCtx, completion); err != nil && s.logger != nil {
		s.logger.Error("保存任务及尝试终态失败，保留中断恢复状态", "task_id", task.ID, "error", err)
	}
}

func (s *Server) finishCancelledTask(task repository.Task, attempt repository.TaskAttempt, ctx context.Context) bool {
	if ctx.Err() != nil {
		s.finishTaskContext(task, attempt, ctx.Err())
		return true
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	persisted, err := s.tasks.GetTask(checkCtx, task.ID)
	if err != nil {
		s.finishTaskBeforeExecution(task, attempt, "failed", "无法确认任务状态，未执行外部操作")
		return true
	}
	if persisted.Status == "cancelled" || persisted.Status == "cancelling" {
		s.finishTaskContext(task, attempt, context.Canceled)
		return true
	}
	return false
}

const certificateTaskTimeout = 30 * time.Minute

func (s *Server) registerTaskCancel(taskID string, cancel context.CancelFunc) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.taskCancels == nil {
		s.taskCancels = make(map[string]context.CancelFunc)
	}
	s.taskCancels[taskID] = cancel
}

func (s *Server) unregisterTaskCancel(taskID string) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	delete(s.taskCancels, taskID)
	delete(s.asyncTasks, taskID)
}

func (s *Server) taskWorkflowContext(c *gin.Context) context.Context {
	ctx := repository.WithTaskCancellationHooks(c.Request.Context(), repository.TaskCancellationHooks{
		Register:   s.registerTaskCancel,
		Unregister: s.unregisterTaskCancel,
	})
	return repository.WithTaskOperator(ctx, s.currentOperatorID(c))
}

func taskRepositoryUnavailable(tasks repository.TaskRepository) bool {
	if tasks == nil {
		return true
	}
	value := reflect.ValueOf(tasks)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
func (s *Server) projectTaskReader(c *gin.Context) (repository.ProjectTaskReader, bool) {
	if taskRepositoryUnavailable(s.tasks) {
		s.writeError(c, http.StatusNotImplemented, "任务管理接口尚未配置", errors.New("任务仓储未配置"))
		return nil, false
	}
	reader, ok := s.tasks.(repository.ProjectTaskReader)
	if !ok {
		s.writeError(c, http.StatusNotImplemented, "任务权限接口尚未配置", errors.New("项目任务读取服务未配置"))
		return nil, false
	}
	return reader, true
}

func (s *Server) cancelTask(c *gin.Context) {
	reader, ok := s.projectTaskReader(c)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(c.Param("id"))
	item, err := reader.GetVisibleTask(c.Request.Context(), c.GetString(authenticatedActorKey), taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权访问此任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	if !isCancellableTaskStatus(item.Status) {
		c.JSON(http.StatusConflict, gin.H{"error": "任务当前无法取消", "task": item})
		return
	}
	if item.TaskType == "renewal_run" {
		c.JSON(http.StatusConflict, gin.H{"error": "该任务由证书工作流控制，不能单独修改取消状态", "task": item})
		return
	}
	if item.TaskType == "management_renewal" {
		c.JSON(http.StatusConflict, gin.H{"error": "管理证书任务由续期调度控制，不能单独修改取消状态", "task": item})
		return
	}
	if item.TaskType == "renewal_check" {
		if err := s.authorizeRenewalOperator(c.Request.Context(), s.currentOperatorID(c)); err != nil {
			s.writeError(c, http.StatusForbidden, "只有当前有效的超级管理员可以取消全局续期检查", nil)
			return
		}
	} else if !s.authorizeProjectPermissions(c, item.ProjectID, domain.PermissionTaskCancel) {
		return
	}
	s.taskMu.Lock()
	if s.asyncTasks[taskID] {
		if err := reader.RequestCancelVisibleTask(c.Request.Context(), c.GetString(authenticatedActorKey), taskID); err != nil {
			s.taskMu.Unlock()
			s.writeError(c, http.StatusConflict, "任务状态已变化，无法取消", err)
			return
		}
		s.taskCancels[taskID]()
		s.taskMu.Unlock()
		item.Status = "cancelling"
		item.Message = "正在取消任务并等待部署收尾"
		if s.audit != nil {
			_ = s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
				TaskID: taskID, OperatorID: s.currentOperatorID(c), Action: "task_cancel",
				ResourceType: "task", ResourceID: taskID, Status: "success", Message: item.Message,
			})
		}
		c.JSON(http.StatusAccepted, gin.H{"task": item})
		return
	}
	s.taskMu.Unlock()
	finished := time.Now().UTC()
	if err := reader.CancelVisibleTask(c.Request.Context(), c.GetString(authenticatedActorKey), taskID, "任务已由用户取消", &finished); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) || errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusForbidden, "无权访问此任务", nil)
			return
		}
		s.writeError(c, http.StatusConflict, "取消任务失败", err)
		return
	}
	s.taskMu.Lock()
	cancel := s.taskCancels[taskID]
	s.taskMu.Unlock()
	if cancel != nil {
		cancel()
	}
	item.Status = "cancelled"
	item.Message = "任务已由用户取消"
	item.FinishedAt = &finished
	item.UpdatedAt = finished
	if s.audit != nil {
		_ = s.audit.CreateAuditLog(context.Background(), repository.AuditLog{
			TaskID: taskID, OperatorID: s.currentOperatorID(c),
			Action: "task_cancel", ResourceType: "task", ResourceID: taskID, Status: "success", Message: item.Message,
		})
	}
	c.JSON(http.StatusOK, gin.H{"task": item})
}

func (s *Server) retryTask(c *gin.Context) {
	reader, ok := s.projectTaskReader(c)
	if !ok {
		return
	}
	actorID := c.GetString(authenticatedActorKey)
	taskID := strings.TrimSpace(c.Param("id"))
	item, err := reader.GetVisibleTask(c.Request.Context(), actorID, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权访问此任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	s.taskMu.Lock()
	workerActive := s.asyncTasks[taskID]
	s.taskMu.Unlock()
	if workerActive {
		s.writeError(c, http.StatusConflict, "任务仍在执行或收尾，暂时不能重试", nil)
		return
	}
	if item.TaskType != "certificate_issue" && item.TaskType != "certificate_deploy" {
		c.JSON(http.StatusConflict, gin.H{"error": "当前任务类型不支持手动重试", "task": item})
		return
	}
	retryable := item.Status == "failed" || item.Status == "cancelled"
	if item.TaskType == "certificate_deploy" && item.Status == "partial_success" {
		retryable = true
	}
	if !retryable {
		c.JSON(http.StatusConflict, gin.H{"error": "仅失败、已取消或部分成功的证书任务可以重试", "task": item})
		return
	}
	var runTask func(*gin.Context, repository.Task, repository.TaskAttempt, string)
	if item.TaskType == "certificate_deploy" {
		if s.deployCertificateTask == nil {
			s.writeError(c, http.StatusNotImplemented, "证书部署接口尚未配置", errors.New("证书部署工作流未配置"))
			return
		}
		runTask = s.executeCertificateDeployTask
	} else {
		if s.issueCertificate == nil {
			s.writeError(c, http.StatusNotImplemented, "证书申请接口尚未配置", errors.New("证书申请工作流未配置"))
			return
		}
		runTask = s.executeCertificateIssueTask
	}
	authorizer, ok := s.projectMembers.(repository.ProjectAuthorizer)
	if !ok {
		s.writeError(c, http.StatusServiceUnavailable, "任务重试权限服务尚未就绪", errors.New("项目授权服务未配置"))
		return
	}
	projectID := item.ProjectID
	for _, permission := range []domain.ProjectPermission{
		domain.PermissionTaskRetry,
		domain.PermissionCertificateManage,
		domain.PermissionDeploymentExecute,
	} {
		if err := authorizer.AuthorizeProject(c.Request.Context(), actorID, projectID, permission); err != nil {
			if errors.Is(err, repository.ErrProjectAccessDenied) {
				s.writeError(c, http.StatusForbidden, "无权重试此项目的证书任务", nil)
				return
			}
			s.writeError(c, http.StatusInternalServerError, "检查任务重试权限失败", err)
			return
		}
	}
	if item.TaskType == "certificate_issue" {
		if !s.requireProjectResourceReader(c) {
			return
		}
		resource, err := s.projectResources.GetVisibleDomain(c.Request.Context(), actorID, item.DomainID)
		if err != nil {
			s.writeProjectResourceError(c, err)
			return
		}
		if !s.authorizeIssueChallenge(c, resource) {
			return
		}
	}
	startedAt := time.Now().UTC()
	retryTask, attempt, err := reader.RetryVisibleTask(c.Request.Context(), actorID, taskID, &startedAt)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrProjectAccessDenied), errors.Is(err, sql.ErrNoRows):
			s.writeError(c, http.StatusForbidden, "无权重试此任务", nil)
		case errors.Is(err, repository.ErrTaskNotRetryable):
			s.writeError(c, http.StatusConflict, "任务状态已变化，当前无法重试", nil)
		default:
			s.writeError(c, http.StatusInternalServerError, "创建任务重试失败", err)
		}
		return
	}
	runTask(c, retryTask, attempt, item.TaskType+"_retry")
}
func validTaskIdempotencyKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

func isActiveTaskStatus(status string) bool {
	switch status {
	case "queued", "running", "retry_wait", "cancelling":
		return true
	default:
		return false
	}
}

func isCancellableTaskStatus(status string) bool {
	switch status {
	case "queued", "running", "retry_wait", "cancelling":
		return true
	default:
		return false
	}
}

func (s *Server) listTasks(c *gin.Context) {
	reader, ok := s.projectTaskReader(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := reader.ListVisibleTasks(c.Request.Context(), c.GetString(authenticatedActorKey), c.Query("domain_id"), limit)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询任务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) getTask(c *gin.Context) {
	reader, ok := s.projectTaskReader(c)
	if !ok {
		return
	}
	item, err := reader.GetVisibleTask(c.Request.Context(), c.GetString(authenticatedActorKey), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权访问此任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) listTaskAttempts(c *gin.Context) {
	reader, ok := s.projectTaskReader(c)
	if !ok {
		return
	}
	actorID := c.GetString(authenticatedActorKey)
	if _, err := reader.GetVisibleTask(c.Request.Context(), actorID, c.Param("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权访问此任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	items, err := reader.ListVisibleTaskAttempts(c.Request.Context(), actorID, c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询任务尝试失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) listTaskDeploymentResults(c *gin.Context) {
	reader, ok := s.projectTaskReader(c)
	if !ok {
		return
	}
	actorID := c.GetString(authenticatedActorKey)
	if _, err := reader.GetVisibleTask(c.Request.Context(), actorID, c.Param("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权访问此任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	items, err := reader.ListVisibleTaskDeploymentResults(c.Request.Context(), actorID, c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询任务部署结果失败", err)
		return
	}
	for index := range items {
		if items[index].Status == "failed" {
			items[index].Message = deployment.PublicStoredFailureMessage(items[index].Message)
		}
	}
	reports, err := reader.ListVisibleTaskDeploymentReports(c.Request.Context(), actorID, c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询完整部署报告失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "reports": reports})
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

func (s *Server) listAuditLogs(c *gin.Context) {
	if !s.requireProjectResourceReader(c) {
		return
	}
	reader, ok := s.projectResources.(repository.ProjectAuditReader)
	if !ok {
		s.writeError(c, http.StatusNotImplemented, "审计日志查询接口尚未配置", errors.New("审计日志读取服务未配置"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := reader.ListVisibleAuditLogs(c.Request.Context(), c.GetString(authenticatedActorKey), limit)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询审计日志失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
