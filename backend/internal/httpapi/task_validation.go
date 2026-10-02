package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) executeCertificateValidationTask(c *gin.Context, task repository.Task, attempt repository.TaskAttempt) {
	if s.enqueueTaskWorker(c, task, attempt, func(ctx context.Context) {
		s.runCertificateOperation(ctx, task, attempt, task.TaskType, func(ctx context.Context, id string) (certificateTaskResult, error) {
			_, validation, err := s.validateCertificateTask(ctx, id)
			if err != nil {
				return certificateTaskResult{}, err
			}
			return certificateTaskResult{Validation: &validation}, nil
		}, "certificate_validate", "正在校验证书、私钥匹配及证书链", "证书校验通过")
	}, nil) {
		writeAcceptedCertificateTask(c, task)
	}
}

func (s *Server) listTaskValidationResults(c *gin.Context) {
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
	validationReader, ok := s.tasks.(repository.TaskValidationReader)
	if !ok {
		s.writeError(c, http.StatusNotImplemented, "任务校验结果接口尚未配置", nil)
		return
	}
	items, err := validationReader.ListVisibleTaskValidations(c.Request.Context(), actorID, c.Param("id"))
	if err != nil {
		s.writeError(c, http.StatusInternalServerError, "查询任务校验结果失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
