package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 持久化任务与尝试后才调用此入口；后台执行不得持有请求或响应对象。
// 进程中断由启动恢复标记失败，不自动重放可能已经签发或部署的外部操作。
func (s *Server) enqueueCertificateTask(c *gin.Context, task repository.Task, attempt repository.TaskAttempt, action string, operation func(context.Context, string) (certificate.IssueResult, deployment.Report, error), stage, runningMessage, successMessage string, cleanup func()) bool {
	return s.enqueueTaskWorker(c, task, attempt, func(ctx context.Context) {
		s.runCertificateTask(ctx, task, attempt, action, operation, stage, runningMessage, successMessage)
	}, cleanup)
}

func (s *Server) enqueueTaskWorker(c *gin.Context, task repository.Task, attempt repository.TaskAttempt, execute func(context.Context), cleanup func()) bool {
	s.taskMu.Lock()
	if s.taskWorkersStopped || len(s.asyncTasks) >= 32 {
		s.taskMu.Unlock()
		s.finishTaskBeforeExecution(task, attempt, "failed", "服务正在停止或任务队列已满，任务未执行")
		s.writeError(c, http.StatusServiceUnavailable, "服务正在停止或任务队列已满，请稍后重试", nil)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), certificateTaskTimeout)
	if s.taskCancels == nil {
		s.taskCancels = make(map[string]context.CancelFunc)
	}
	if s.asyncTasks == nil {
		s.asyncTasks = make(map[string]bool)
		s.taskSlots = make(chan struct{}, 4)
	}
	s.taskCancels[task.ID] = cancel
	s.asyncTasks[task.ID] = true
	s.taskWorkers.Add(1)
	s.taskMu.Unlock()
	go func() {
		defer s.taskWorkers.Done()
		if cleanup != nil {
			defer cleanup()
		}
		defer s.unregisterTaskCancel(task.ID)
		defer cancel()
		defer func() {
			if recover() != nil {
				message := "任务执行异常中断，请检查部署恢复状态后重试"
				if task.TaskType == "certificate_validate" {
					message = "证书校验异常中断，未保存通过结果"
				}
				if task.TaskType == "renewal_check" {
					message = "续期检查异常中断，请检查已生成的续期任务，不会自动重放"
				}
				s.finishTaskBeforeExecution(task, attempt, "failed", message)
			}
		}()
		select {
		case s.taskSlots <- struct{}{}:
			defer func() { <-s.taskSlots }()
		case <-ctx.Done():
			s.finishTaskContext(task, attempt, ctx.Err())
			return
		}
		execute(ctx)
	}()
	return true
}

// 上传文件所有权完成转交后再写响应；客户端断开或响应异常不得回收工作线程的文件。
func writeAcceptedCertificateTask(c *gin.Context, task repository.Task) {
	c.Header("Location", "/api/v1/tasks/"+task.ID)
	c.JSON(http.StatusAccepted, gin.H{"task": task, "accepted": true})
}

func (s *Server) finishTaskContext(task repository.Task, attempt repository.TaskAttempt, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		s.finishTaskBeforeExecution(task, attempt, "failed", "任务执行超时，未继续执行")
		return
	}
	s.finishTaskBeforeExecution(task, attempt, "cancelled", "任务已取消，未继续执行")
}

func (s *Server) finishTaskBeforeExecution(task repository.Task, attempt repository.TaskAttempt, status, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := time.Now().UTC()
	if err := s.tasks.FinishTaskAttempt(ctx, repository.TaskCompletion{
		TaskID: task.ID, AttemptID: attempt.ID, Status: status, Stage: attempt.Stage,
		Message: message, FinishedAt: finished,
		AuditAction: task.TaskType,
	}); err != nil && s.logger != nil {
		s.logger.Error("保存任务及尝试终态失败，保留中断恢复状态", "task_id", task.ID, "error", err)
	}
}

func (s *Server) stopTaskWorkers() {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	s.taskWorkersStopped = true
	for _, cancel := range s.taskCancels {
		cancel()
	}
}

func (s *Server) waitTaskWorkers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.taskWorkers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
