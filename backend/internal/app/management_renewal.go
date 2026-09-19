package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/notification"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const managementResourceID = "management-certificate"

type managementActivator interface {
	Ready(context.Context, httpapi.ManagementCertificateState) error
	Activate(context.Context, httpapi.ManagementCertificateState, certificate.SavedResource) error
}

type managementRenewal struct {
	readState   func() (httpapi.ManagementCertificateState, error)
	issue       func(context.Context, certificate.SetupCertificateRequest) (certificate.SavedResource, error)
	activator   managementActivator
	tasks       repository.TaskRepository
	audit       repository.AuditRepository
	notifier    notification.Notifier
	notifyError func(error)
	wait        func(context.Context, time.Duration) error
	mu          sync.Mutex
}

func (m *managementRenewal) RunRenewal(ctx context.Context, policy certificate.RenewalPolicy, now time.Time) (certificate.RenewalReport, error) {
	var report certificate.RenewalReport
	if m == nil || m.readState == nil || m.issue == nil || m.activator == nil || m.tasks == nil || m.audit == nil {
		return report, errors.New("管理证书续期服务未初始化")
	}
	if !m.mu.TryLock() {
		return report, errors.New("管理证书续期任务正在执行")
	}
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	state, err := m.readState()
	// 公网 IP 自签证书不进入域名的 ACME DNS 续期流程。
	if state.Hostname == "" {
		return report, nil
	}
	report.Checked = 1
	if err == nil && state.NotAfter.IsZero() {
		err = errors.New("管理证书到期时间无效")
	}
	if err == nil {
		remaining := state.NotAfter.Sub(now)
		if remaining <= policy.NearExpiryThreshold {
			report.NearExpiry = 1
		}
		if remaining > policy.RenewalThreshold {
			return report, nil
		}
		report.Selected = 1
		if remaining <= policy.UrgentThreshold {
			report.Urgent = 1
		}
	}
	saved, err := m.renew(ctx, state, policy, report.Urgent != 0, err)
	if err != nil {
		report.Failed = 1
		report.Errors = []string{state.Hostname + "：管理证书续期未完成，请检查任务记录"}
		if m.notifyError != nil {
			m.notifyError(err)
		}
	} else {
		report.Succeeded = 1
	}
	m.notify(ctx, state, saved, err)
	return report, err
}

func (m *managementRenewal) renew(ctx context.Context, state httpapi.ManagementCertificateState, policy certificate.RenewalPolicy, urgent bool, stateErr error) (saved certificate.SavedResource, resultErr error) {
	started := time.Now().UTC()
	task := repository.Task{
		ID: domain.NewID(), DomainID: managementResourceID, TaskType: "management_renewal",
		Status: "running", Stage: "preflight", Message: "正在检查管理证书续期条件",
		StartedAt: &started, CreatedAt: started,
	}
	if err := m.tasks.CreateTask(ctx, task); err != nil {
		return saved, err
	}
	stage := "preflight"
	defer func() {
		// 取消请求或关闭服务后仍需保存终态，但不无限等待数据库。
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		status, message := "success", "管理证书续期及入口激活完成"
		if resultErr != nil {
			status, message = "failed", "管理证书续期未完成，请检查服务日志"
			if errors.Is(resultErr, context.Canceled) {
				status, message = "cancelled", "管理证书续期已取消"
			}
		}
		finished := time.Now().UTC()
		taskErr := m.tasks.UpdateTask(finishCtx, task.ID, status, stage, message, &started, &finished)
		auditErr := m.audit.CreateAuditLog(finishCtx, repository.AuditLog{
			Action: "management_renewal", ResourceType: "management_certificate", ResourceID: managementResourceID,
			Status: status, Message: state.Hostname + "：" + message,
		})
		resultErr = errors.Join(resultErr, taskErr, auditErr)
	}()
	if err := m.audit.CreateAuditLog(ctx, repository.AuditLog{
		Action: "management_renewal", ResourceType: "management_certificate", ResourceID: managementResourceID,
		Status: "running", Message: state.Hostname + "：开始管理证书续期",
	}); err != nil {
		return saved, err
	}
	if stateErr != nil {
		return saved, fmt.Errorf("读取管理证书续期状态失败：%w", stateErr)
	}
	if !state.DNSVerified {
		return saved, errors.New("管理证书 DNS 凭据尚未验证")
	}
	if err := m.activator.Ready(ctx, state); err != nil {
		return saved, err
	}
	attempts := 1
	if urgent && policy.MaxRetries > 1 {
		attempts = policy.MaxRetries
	}
	for number := 1; number <= attempts; number++ {
		if err := ctx.Err(); err != nil {
			return saved, err
		}
		saved, stage, resultErr = m.attempt(ctx, task, state, number)
		// 激活和持久化失败不重复向 CA 申请，候选文件保留供诊断或回退。
		if resultErr == nil || stage != "issue" || number == attempts {
			return saved, resultErr
		}
		stage = "retry_wait"
		if err := m.tasks.UpdateTask(ctx, task.ID, "running", stage, "管理证书申请失败，等待重试", &started, nil); err != nil {
			return saved, errors.Join(resultErr, err)
		}
		wait := m.wait
		if wait == nil {
			wait = waitForManagementRetry
		}
		if err := wait(ctx, policy.RetryInterval); err != nil {
			return saved, errors.Join(resultErr, err)
		}
	}
	return saved, resultErr
}

func (m *managementRenewal) attempt(ctx context.Context, task repository.Task, state httpapi.ManagementCertificateState, number int) (saved certificate.SavedResource, stage string, resultErr error) {
	stage = "record"
	started := time.Now().UTC()
	attempt := repository.TaskAttempt{
		ID: domain.NewID(), TaskID: task.ID, AttemptNumber: number, Stage: "management_renewal",
		Status: "running", Message: "正在申请并激活管理证书", StartedAt: &started, CreatedAt: started,
	}
	if err := m.tasks.CreateAttempt(ctx, attempt); err != nil {
		return saved, stage, err
	}
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		finished := time.Now().UTC()
		status, message := "success", "管理证书已激活"
		if resultErr != nil {
			status, message = "failed", "管理证书处理失败，阶段："+stage
			if errors.Is(resultErr, context.Canceled) {
				status = "cancelled"
			}
		}
		if err := m.tasks.FinishAttempt(finishCtx, attempt.ID, status, message, &finished); err != nil {
			stage = "record"
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if err := m.tasks.UpdateTask(ctx, task.ID, "running", "issue", "正在申请管理证书", task.StartedAt, nil); err != nil {
		return saved, stage, err
	}
	stage = "issue"
	issueCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	saved, resultErr = m.issue(issueCtx, state.Request)
	cancel()
	if resultErr != nil {
		return saved, stage, resultErr
	}
	stage = "activate"
	if err := ctx.Err(); err != nil {
		return saved, stage, err
	}
	if !saved.NotAfter.After(state.NotAfter) {
		return saved, stage, errors.New("候选管理证书未延长有效期，已拒绝激活")
	}
	if err := m.tasks.UpdateTask(ctx, task.ID, "running", stage, "正在激活管理证书", task.StartedAt, nil); err != nil {
		return saved, stage, err
	}
	return saved, stage, m.activator.Activate(ctx, state, saved)
}

func (m *managementRenewal) notify(ctx context.Context, state httpapi.ManagementCertificateState, saved certificate.SavedResource, renewalErr error) {
	if m.notifier == nil {
		return
	}
	event := notification.Event{
		Type: "management_renewal", Status: "success", DomainID: managementResourceID,
		Domain: state.Hostname, Domains: []string{state.Hostname}, ManagementURL: state.ManagementURL,
		ExpiresAt: &saved.NotAfter, CertificateSerial: saved.SerialNumber,
	}
	if renewalErr != nil {
		event.Status = "failed"
		event.Error = "管理证书续期未完成，请检查任务记录和服务日志"
		event.ExpiresAt = &state.NotAfter
		event.CertificateSerial = ""
	}
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := m.notifier.Send(notifyCtx, event); err != nil && m.notifyError != nil {
		m.notifyError(fmt.Errorf("管理证书通知发送失败：%w", err))
	}
}

func waitForManagementRetry(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type directManagementActivator struct {
	server *httpapi.Server
}

func requireManagementListener(ctx context.Context, server *httpapi.Server) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if server != nil {
		select {
		case <-server.Listening():
			return nil
		default:
		}
	}
	return errors.New("当前进程尚未启动管理 HTTPS 监听，已阻止申请管理证书")
}

func (a directManagementActivator) Ready(ctx context.Context, state httpapi.ManagementCertificateState) error {
	if err := requireManagementListener(ctx, a.server); err != nil {
		return err
	}
	if state.HTTPSMode != "direct" {
		return errors.New("管理证书代理激活器尚未配置，已阻止申请和上游单独切换")
	}
	return nil
}

func (a directManagementActivator) Activate(ctx context.Context, state httpapi.ManagementCertificateState, saved certificate.SavedResource) error {
	if err := a.Ready(ctx, state); err != nil {
		return err
	}
	return a.server.ActivateManagementCertificate(state, saved)
}
