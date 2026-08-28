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
	domainID := strings.TrimSpace(c.Param("id"))
	if _, err := s.repository.GetByID(c.Request.Context(), domainID); err != nil {
		if repository.IsNotFound(err) {
			s.writeError(c, http.StatusNotFound, "域名不存在", err)
			return
		}
		s.writeError(c, http.StatusInternalServerError, "读取域名失败", err)
		return
	}
	if active, err := s.tasks.FindActiveTaskByDomain(c.Request.Context(), domainID, "certificate_issue"); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "该域名已有正在执行的证书任务", "task": active})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.writeError(c, http.StatusInternalServerError, "检查正在执行的任务失败", err)
		return
	}
	now := time.Now().UTC()
	task := repository.Task{
		ID:        domain.NewID(),
		DomainID:  domainID,
		TaskType:  "certificate_issue",
		Status:    "queued",
		Stage:     "queued",
		Message:   "等待执行证书申请",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.tasks.CreateTask(c.Request.Context(), task); err != nil {
		s.writeError(c, http.StatusInternalServerError, "创建证书任务失败", err)
		return
	}
	attempt := repository.TaskAttempt{
		ID:            domain.NewID(),
		TaskID:        task.ID,
		AttemptNumber: 1,
		Stage:         "certificate_issue",
		Status:        "running",
		Message:       "开始申请证书",
		StartedAt:     &now,
		CreatedAt:     now,
	}
	if err := s.tasks.CreateAttempt(c.Request.Context(), attempt); err != nil {
		_ = s.tasks.UpdateTask(context.Background(), task.ID, "failed", "certificate_issue", "创建任务尝试失败", &now, &now)
		s.writeError(c, http.StatusInternalServerError, "创建任务尝试失败", err)
		return
	}
	if err := s.tasks.UpdateTask(c.Request.Context(), task.ID, "running", "certificate_issue", "正在申请证书并部署", &now, nil); err != nil {
		s.writeError(c, http.StatusInternalServerError, "更新任务状态失败", err)
		return
	}
	certificateResult, deploymentReport, issueErr := s.issueCertificate(c.Request.Context(), domainID)
	finished := time.Now().UTC()
	status := "success"
	message := "证书申请和部署完成"
	if issueErr != nil {
		status = "failed"
		message = issueErr.Error()
	}
	_ = s.tasks.UpdateTask(context.Background(), task.ID, status, "certificate_issue", message, &now, &finished)
	_ = s.tasks.FinishAttempt(context.Background(), attempt.ID, status, message, &finished)
	if s.audit != nil {
		auditStatus := status
		if issueErr != nil {
			auditStatus = "error"
		}
		_ = s.audit.CreateAuditLog(context.Background(), repository.AuditLog{
			Action:       "certificate_issue",
			ResourceType: "domain",
			ResourceID:   domainID,
			Status:       auditStatus,
			Message:      message,
		})
	}
	response := gin.H{
		"task":        task,
		"certificate": certificateResult,
		"deployment":  deploymentReport,
		"task_status": status,
		"finished_at": finished,
	}
	if issueErr != nil {
		response["error"] = issueErr.Error()
		c.JSON(http.StatusBadGateway, response)
		return
	}
	task.Status = status
	task.Stage = "certificate_issue"
	task.Message = message
	task.StartedAt = &now
	task.FinishedAt = &finished
	task.UpdatedAt = finished
	response["task"] = task
	c.JSON(http.StatusOK, response)
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
