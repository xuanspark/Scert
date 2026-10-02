package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 全局检查不是业务域名，也不是单次管理证书签发；子续期仍由原调度器负责。
func (s *Server) submitRenewalTask(c *gin.Context) {
	operatorID := s.currentOperatorID(c)
	if err := s.authorizeRenewalOperator(c.Request.Context(), operatorID); err != nil {
		s.writeError(c, http.StatusForbidden, "只有当前有效的超级管理员可以执行全局续期检查", nil)
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key != "" && !validTaskIdempotencyKey(key) {
		s.writeError(c, http.StatusBadRequest, "幂等键格式无效", nil)
		return
	}
	s.issueTaskMu.Lock()
	defer s.issueTaskMu.Unlock()
	if key != "" {
		item, err := s.tasks.FindTaskByIdempotencyKey(c.Request.Context(), repository.RenewalCheckResourceID, key)
		if err == nil {
			if item.TaskType != "renewal_check" || item.ScopeType != repository.ScopeSystem {
				s.writeError(c, http.StatusConflict, "幂等键已用于另一种操作", nil)
				return
			}
			c.JSON(http.StatusOK, gin.H{"task": item, "replayed": true})
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			s.writeError(c, http.StatusInternalServerError, "检查续期幂等任务失败", err)
			return
		}
	}
	if active, err := s.tasks.FindActiveTaskByDomain(c.Request.Context(), repository.RenewalCheckResourceID, "renewal_check"); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "全局续期检查已在执行，请查看现有任务", "task": active, "replayed": true})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.writeError(c, http.StatusInternalServerError, "检查活动续期任务失败", err)
		return
	}
	now := time.Now().UTC()
	task := repository.Task{
		HistoryScope: repository.HistoryScope{ScopeType: repository.ScopeSystem},
		ID:           domain.NewID(), DomainID: repository.RenewalCheckResourceID, TaskType: "renewal_check",
		Status: "queued", Stage: "queued", Message: "等待检查管理证书和业务域名续期",
		OperatorID: operatorID, IdempotencyKey: key, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.tasks.CreateTask(c.Request.Context(), task); err != nil {
		s.writeError(c, http.StatusInternalServerError, "创建续期检查任务失败", err)
		return
	}
	attempt := repository.TaskAttempt{
		ID: domain.NewID(), TaskID: task.ID, AttemptNumber: 1, Stage: "renewal_check",
		Status: "running", Message: task.Message, CreatedAt: now,
	}
	if err := s.tasks.CreateAttempt(c.Request.Context(), attempt); err != nil {
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if finishErr := s.tasks.FinishTaskAttempt(finishCtx, repository.TaskCompletion{
			TaskID: task.ID, Status: "failed", Stage: "record", Message: "创建续期尝试失败，未执行续期",
			FinishedAt: time.Now().UTC(), WithoutAttempt: true, AuditAction: task.TaskType,
		}); finishErr != nil && s.logger != nil {
			s.logger.Error("续期任务收尾失败，保留启动恢复状态", "task_id", task.ID, "error", finishErr)
		}
		s.writeError(c, http.StatusInternalServerError, "创建续期尝试失败", err)
		return
	}
	if s.enqueueTaskWorker(c, task, attempt, func(ctx context.Context) {
		s.runRenewalTask(ctx, task, attempt)
	}, nil) {
		writeAcceptedCertificateTask(c, task)
	}
}

func (s *Server) authorizeRenewalOperator(ctx context.Context, operatorID string) error {
	if s.users == nil || operatorID == "" {
		return repository.ErrProjectAccessDenied
	}
	user, err := s.users.GetUserByID(ctx, operatorID)
	if err != nil {
		return err
	}
	if user.Role != repository.UserRoleSuperAdmin || user.Status != repository.UserStatusActive {
		return repository.ErrProjectAccessDenied
	}
	return nil
}

func (s *Server) runRenewalTask(ctx context.Context, task repository.Task, attempt repository.TaskAttempt) {
	if s.finishCancelledTask(task, attempt, ctx) {
		return
	}
	if err := s.authorizeRenewalOperator(ctx, task.OperatorID); err != nil {
		attempt.Stage = "authorize"
		s.finishTaskBeforeExecution(task, attempt, "failed", "续期执行前超级管理员权限校验未通过，未执行续期")
		return
	}
	now := time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task.ID, "running", "renewal_check", "正在检查管理证书和业务域名续期", &now, nil); err != nil {
		if !s.finishCancelledTask(task, attempt, ctx) {
			s.finishTaskBeforeExecution(task, attempt, "failed", "续期检查启动失败，未执行续期")
		}
		return
	}
	if s.audit != nil {
		if err := s.audit.CreateAuditLog(ctx, repository.AuditLog{
			TaskID: task.ID, OperatorID: task.OperatorID, Action: task.TaskType,
			ResourceType: "task", ResourceID: task.ID, Status: "running", Message: "开始全局续期检查",
		}); err != nil {
			s.finishTaskBeforeExecution(task, attempt, "failed", "记录续期开始审计失败，未执行续期")
			return
		}
	}
	if s.finishCancelledTask(task, attempt, ctx) {
		return
	}
	// 不继承父任务事务编号，原续期工作流自行生成子任务及部署事务。
	executionCtx := repository.WithTaskOperator(ctx, task.OperatorID)
	executionCtx = repository.WithTaskCancellationHooks(executionCtx, repository.TaskCancellationHooks{
		Register: s.registerTaskCancel, Unregister: s.unregisterTaskCancel,
	})
	report, runErr := s.runRenewal(executionCtx)
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	status, prefix := "success", "续期检查完成"
	if runErr != nil || report.Failed > 0 || len(report.Errors) > 0 {
		status, prefix = "failed", "续期检查未全部完成"
		if report.Succeeded > 0 {
			status = "partial_success"
		}
	}
	// 即使下游忽略取消并返回成功，也不得将取消或超时伪装为整体成功。
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded) {
		status, prefix = "failed", "续期检查超时，以下为已执行统计"
	} else if errors.Is(ctx.Err(), context.Canceled) || errors.Is(runErr, context.Canceled) {
		status, prefix = "cancelled", "续期检查已取消，以下为已执行统计"
	}
	message := fmt.Sprintf("%s：检查 %d 项，临期 %d 项，选中 %d 项，紧急 %d 项，成功 %d 项，失败 %d 项，异常 %d 项；逐项结果请查看续期任务",
		prefix, report.Checked, report.NearExpiry, report.Selected, report.Urgent, report.Succeeded, report.Failed, len(report.Errors))
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tasks.FinishTaskAttempt(finishCtx, repository.TaskCompletion{
		TaskID: task.ID, AttemptID: attempt.ID, Status: status, Stage: "renewal_check",
		Message: message, StartedAt: &now, FinishedAt: time.Now().UTC(), AuditAction: task.TaskType,
	}); err != nil && s.logger != nil {
		s.logger.Error("保存续期检查终态失败，保留启动恢复状态", "task_id", task.ID, "error", err)
	}
}
