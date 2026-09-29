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

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) issueDomainAsTask(c *gin.Context) {
	if s.issueCertificate == nil {
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
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), resource.ResourceScope.Normalize().ProjectID, domain.PermissionCertificateManage); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权管理此项目的证书任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "检查证书任务权限失败", err)
		return
	}
	if err := authorizer.AuthorizeProject(c.Request.Context(), c.GetString(authenticatedActorKey), resource.ResourceScope.Normalize().ProjectID, domain.PermissionDeploymentExecute); err != nil {
		if errors.Is(err, repository.ErrProjectAccessDenied) {
			s.writeError(c, http.StatusForbidden, "无权执行此项目的证书部署任务", nil)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "检查证书部署权限失败", err)
		return
	}
	if !s.authorizeDNSCredentialUse(c, resource.ResourceScope.Normalize().ProjectID) {
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
			status := http.StatusOK
			if existing.Status == "queued" || existing.Status == "running" {
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
	if active, err := s.tasks.FindActiveTaskByDomain(c.Request.Context(), domainID, "certificate_issue"); err == nil {
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
		ID:           domain.NewID(), DomainID: domainID, TaskType: "certificate_issue", Status: "queued", Stage: "queued",
		Message: "等待执行证书申请", IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.tasks.CreateTask(c.Request.Context(), task); err != nil {
		s.issueTaskMu.Unlock()
		s.writeError(c, http.StatusInternalServerError, "创建证书任务失败", err)
		return
	}
	s.issueTaskMu.Unlock()
	attempt := repository.TaskAttempt{
		ID: domain.NewID(), TaskID: task.ID, AttemptNumber: 1, Stage: "certificate_issue", Status: "running",
		Message: "开始申请证书", StartedAt: &now, CreatedAt: now,
	}
	if err := s.tasks.CreateAttempt(c.Request.Context(), attempt); err != nil {
		_ = s.tasks.UpdateTask(context.Background(), task.ID, "failed", "certificate_issue", "创建任务尝试失败", &now, &now)
		s.writeError(c, http.StatusInternalServerError, "创建任务尝试失败", err)
		return
	}
	s.executeCertificateIssueTask(c, task, attempt, "certificate_issue")
}

func (s *Server) executeCertificateIssueTask(c *gin.Context, task repository.Task, attempt repository.TaskAttempt, auditAction string) {
	now := time.Now().UTC()
	if err := s.tasks.UpdateTask(c.Request.Context(), task.ID, "running", "certificate_issue", "正在申请证书并部署", &now, nil); err != nil {
		finished := time.Now().UTC()
		failureMessage := "任务启动失败"
		_ = s.tasks.UpdateTask(context.Background(), task.ID, "failed", "certificate_issue", failureMessage, &now, &finished)
		_ = s.tasks.FinishAttempt(context.Background(), attempt.ID, "failed", failureMessage, &finished)
		if s.audit != nil {
			_ = s.audit.CreateAuditLog(context.Background(), repository.AuditLog{
				TaskID: task.ID, OperatorID: task.OperatorID,
				Action: auditAction, ResourceType: "domain", ResourceID: task.DomainID,
				Status: "error", Message: failureMessage,
			})
		}
		s.writeError(c, http.StatusInternalServerError, "更新任务状态失败", err)
		return
	}
	executionCtx, cancel := context.WithTimeout(c.Request.Context(), certificateTaskTimeout)
	s.registerTaskCancel(task.ID, cancel)
	defer func() {
		cancel()
		s.unregisterTaskCancel(task.ID)
	}()
	certificateResult, deploymentReport, issueErr := s.issueCertificate(executionCtx, task.DomainID)
	deploymentReport = sanitizeDeploymentReport(deploymentReport)
	finished := time.Now().UTC()
	status := "success"
	message := "证书申请和部署完成"
	cancelled := false
	if issueErr != nil {
		status = "failed"
		message = certificateTaskErrorMessage(issueErr)
		if s.logger != nil {
			s.logger.Error("证书任务执行失败", "task_id", task.ID, "domain_id", task.DomainID, "error", issueErr)
		}
		if errors.Is(issueErr, context.Canceled) {
			status = "cancelled"
		}
	}
	if persisted, err := s.tasks.GetTask(context.Background(), task.ID); err == nil && persisted.Status == "cancelled" {
		status = "cancelled"
		message = persisted.Message
		cancelled = true
		if issueErr == nil {
			issueErr = errors.New(message)
		}
	}
	if !cancelled {
		_ = s.tasks.UpdateTask(context.Background(), task.ID, status, "certificate_issue", message, &now, &finished)
	}
	_ = s.tasks.FinishAttempt(context.Background(), attempt.ID, status, message, &finished)
	task.Status = status
	task.Stage = "certificate_issue"
	task.Message = message
	task.StartedAt = &now
	task.FinishedAt = &finished
	task.UpdatedAt = finished
	if s.audit != nil {
		auditStatus := status
		if issueErr != nil && status != "cancelled" {
			auditStatus = "error"
		}
		_ = s.audit.CreateAuditLog(context.Background(), repository.AuditLog{
			TaskID: task.ID, OperatorID: task.OperatorID,
			Action: auditAction, ResourceType: "domain", ResourceID: task.DomainID, Status: auditStatus, Message: message,
		})
	}
	response := gin.H{
		"task": task, "certificate": certificateResult, "deployment": deploymentReport,
		"task_status": status, "finished_at": finished,
	}
	if issueErr != nil {
		response["error"] = message
		responseStatus := http.StatusBadGateway
		if status == "cancelled" {
			responseStatus = http.StatusConflict
		} else if errors.Is(issueErr, context.DeadlineExceeded) {
			responseStatus = http.StatusGatewayTimeout
		}
		c.JSON(responseStatus, response)
		return
	}
	c.JSON(http.StatusOK, response)
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
	if item.Status != "queued" && item.Status != "running" {
		c.JSON(http.StatusConflict, gin.H{"error": "任务当前无法取消", "task": item})
		return
	}
	if item.TaskType == "renewal_run" || item.TaskType == "certificate_deploy" {
		c.JSON(http.StatusConflict, gin.H{"error": "该任务由证书工作流控制，不能单独修改取消状态", "task": item})
		return
	}
	if item.TaskType == "management_renewal" {
		c.JSON(http.StatusConflict, gin.H{"error": "管理证书任务由续期调度控制，不能单独修改取消状态", "task": item})
		return
	}
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
	if s.issueCertificate == nil {
		s.writeError(c, http.StatusNotImplemented, "证书申请接口尚未配置", errors.New("证书申请工作流未配置"))
		return
	}
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
	if item.TaskType != "certificate_issue" {
		c.JSON(http.StatusConflict, gin.H{"error": "当前任务类型不支持手动重试", "task": item})
		return
	}
	if item.Status != "failed" && item.Status != "cancelled" {
		c.JSON(http.StatusConflict, gin.H{"error": "仅失败或已取消的证书申请任务可以重试", "task": item})
		return
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
	if !s.authorizeDNSCredentialUse(c, projectID) {
		return
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
	s.executeCertificateIssueTask(c, retryTask, attempt, "certificate_issue_retry")
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
