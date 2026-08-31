package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
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
	if s.repository == nil || s.tasks == nil {
		s.writeError(c, http.StatusNotImplemented, "证书任务接口尚未配置", errors.New("域名仓储或任务仓储未配置"))
		return
	}
	domainID := strings.TrimSpace(c.Param("id"))
	if _, err := s.repository.GetByID(c.Request.Context(), domainID); err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取域名失败", err)
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
		ID: domain.NewID(), DomainID: domainID, TaskType: "certificate_issue", Status: "queued", Stage: "queued",
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
	if err := s.tasks.UpdateTask(c.Request.Context(), task.ID, "running", "certificate_issue", "正在申请证书并部署", &now, nil); err != nil {
		finished := time.Now().UTC()
		failureMessage := "任务启动失败"
		_ = s.tasks.UpdateTask(context.Background(), task.ID, "failed", "certificate_issue", failureMessage, &now, &finished)
		_ = s.tasks.FinishAttempt(context.Background(), attempt.ID, "failed", failureMessage, &finished)
		if s.audit != nil {
			_ = s.audit.CreateAuditLog(context.Background(), repository.AuditLog{
				Action: "certificate_issue", ResourceType: "domain", ResourceID: domainID,
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
	certificateResult, deploymentReport, issueErr := s.issueCertificate(executionCtx, domainID)
	deploymentReport = sanitizeDeploymentReport(deploymentReport)
	finished := time.Now().UTC()
	status := "success"
	message := "证书申请和部署完成"
	cancelled := false
	if issueErr != nil {
		status = "failed"
		message = certificateTaskErrorMessage(issueErr)
		if s.logger != nil {
			s.logger.Error("证书任务执行失败", "task_id", task.ID, "domain_id", domainID, "error", issueErr)
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
			Action: "certificate_issue", ResourceType: "domain", ResourceID: domainID, Status: auditStatus, Message: message,
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

func (s *Server) cancelTask(c *gin.Context) {
	if s.tasks == nil {
		s.writeError(c, http.StatusNotImplemented, "任务管理接口尚未配置", errors.New("任务仓储未配置"))
		return
	}
	taskID := strings.TrimSpace(c.Param("id"))
	item, err := s.tasks.GetTask(c.Request.Context(), taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusNotFound, "任务不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	if item.Status != "queued" && item.Status != "running" {
		c.JSON(http.StatusConflict, gin.H{"error": "任务当前无法取消", "task": item})
		return
	}
	finished := time.Now().UTC()
	if err := s.tasks.CancelTask(c.Request.Context(), taskID, "任务已由用户取消", &finished); err != nil {
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
			Action: "task_cancel", ResourceType: "task", ResourceID: taskID, Status: "success", Message: item.Message,
		})
	}
	c.JSON(http.StatusOK, gin.H{"task": item})
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
	if s.tasks == nil {
		s.writeError(c, http.StatusNotImplemented, "任务管理接口尚未配置", errors.New("任务仓储未配置"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := s.tasks.ListTasks(c.Request.Context(), c.Query("domain_id"), limit)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询任务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) getTask(c *gin.Context) {
	if s.tasks == nil {
		s.writeError(c, http.StatusNotImplemented, "任务管理接口尚未配置", errors.New("任务仓储未配置"))
		return
	}
	item, err := s.tasks.GetTask(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusNotFound, "任务不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) listTaskAttempts(c *gin.Context) {
	if s.tasks == nil {
		s.writeError(c, http.StatusNotImplemented, "任务管理接口尚未配置", errors.New("任务仓储未配置"))
		return
	}
	if _, err := s.tasks.GetTask(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusNotFound, "任务不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取任务失败", err)
		return
	}
	items, err := s.tasks.ListAttempts(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询任务尝试失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) listAuditLogs(c *gin.Context) {
	reader, ok := s.audit.(repository.AuditReader)
	if !ok {
		s.writeError(c, http.StatusNotImplemented, "审计日志查询接口尚未配置", errors.New("审计日志读取服务未配置"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := reader.ListAuditLogs(c.Request.Context(), limit)
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询审计日志失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
