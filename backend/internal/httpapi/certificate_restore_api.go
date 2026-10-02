package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func (s *Server) restoreCertificateVersion(c *gin.Context) {
	if !s.authorizeVisibleDomainPermission(c, domain.PermissionDeploymentRollback) {
		return
	}
	domainID := strings.TrimSpace(c.Param("id"))
	version := strings.TrimSpace(c.Param("version"))
	if s.restoreVersionTask != nil && !taskRepositoryUnavailable(s.tasks) {
		s.submitPreparedDomainTask(c, "certificate_restore", &preparedCertificateTask{
			operation: func(ctx context.Context, id string) (certificate.IssueResult, deployment.Report, error) {
				result, err := s.restoreVersionTask(ctx, id, version)
				return result, deployment.Report{}, err
			},
		})
		return
	}
	if s.restoreVersion == nil {
		s.writeError(c, http.StatusNotImplemented, "证书恢复接口尚未配置", errors.New("证书恢复流程未配置"))
		return
	}
	result, err := s.restoreVersion(s.taskWorkflowContext(c), domainID, version)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("恢复证书历史版本失败", "domain_id", c.Param("id"), "version", version, "error", err)
		}
		if s.audit != nil {
			_ = s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
				OperatorID: s.currentOperatorID(c),
				Action:     "certificate_version_restore", ResourceType: "certificate", ResourceID: c.Param("id"), Status: "failed",
				Message: "恢复证书历史版本失败",
			})
		}
		s.writeError(c, certificateErrorStatus(err), "恢复证书历史版本失败", err)
		return
	}
	if s.audit != nil {
		_ = s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
			OperatorID: s.currentOperatorID(c),
			Action:     "certificate_version_restore", ResourceType: "certificate", ResourceID: c.Param("id"), Status: "success",
			Message: "已恢复证书历史版本：" + version,
		})
	}
	c.JSON(http.StatusOK, result)
}
