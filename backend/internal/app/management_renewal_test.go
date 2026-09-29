package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/notification"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type managementTestActivator struct {
	readyErr    error
	activateErr error
	calls       int
}

func (a *managementTestActivator) Ready(context.Context, httpapi.ManagementCertificateState) error {
	return a.readyErr
}

func (a *managementTestActivator) Activate(context.Context, httpapi.ManagementCertificateState, certificate.SavedResource) error {
	a.calls++
	return a.activateErr
}

type managementTestNotifier struct {
	events []notification.Event
	err    error
}

func (n *managementTestNotifier) Send(_ context.Context, event notification.Event) error {
	n.events = append(n.events, event)
	return n.err
}

type managementFixture struct {
	m         *managementRenewal
	app       *App
	state     httpapi.ManagementCertificateState
	policy    certificate.RenewalPolicy
	activator *managementTestActivator
	notifier  *managementTestNotifier
	now       time.Time
	issues    int
}

func newManagementFixture(t *testing.T) *managementFixture {
	t.Helper()
	cfg := readyApplicationConfig(t)
	a, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	f := &managementFixture{
		app: a, now: time.Now().UTC(), activator: &managementTestActivator{}, notifier: &managementTestNotifier{},
		policy: certificate.RenewalPolicy{
			NearExpiryThreshold: 10 * 24 * time.Hour, RenewalThreshold: 6 * 24 * time.Hour,
			UrgentThreshold: 3 * 24 * time.Hour, MaxRetries: 3, RetryInterval: time.Nanosecond,
		},
	}
	f.state = httpapi.ManagementCertificateState{
		Hostname: "random-admin.example.test", NotAfter: f.now.Add(4 * 24 * time.Hour),
		HTTPSMode: "direct", DNSVerified: true, ManagementURL: "https://random-admin.example.test/",
		Request: certificate.SetupCertificateRequest{
			Domains: []string{"random-admin.example.test"}, CertificateType: "single",
			ProviderID: "aliyun", CredentialRef: "secret://dns/test", Environment: "production",
		},
	}
	f.m = &managementRenewal{
		readState: func() (httpapi.ManagementCertificateState, error) { return f.state, nil },
		issue: func(ctx context.Context, request certificate.SetupCertificateRequest) (certificate.SavedResource, error) {
			if ctx.Err() != nil || len(request.Domains) != 1 || request.Domains[0] != f.state.Hostname || request.CredentialRef != f.state.Request.CredentialRef {
				t.Fatalf("签发请求未使用当前管理配置：%+v", request)
			}
			f.issues++
			return certificate.SavedResource{NotAfter: f.now.Add(90 * 24 * time.Hour), SerialNumber: "123"}, nil
		},
		activator: f.activator, tasks: a.Tasks,
		audit: repository.NewSQLAuditRepository(a.Database.DB), notifier: f.notifier,
	}
	return f
}

func (f *managementFixture) assertTask(t *testing.T, status string, attempts int) repository.Task {
	t.Helper()
	items, err := f.app.Tasks.ListTasks(t.Context(), managementResourceID, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("管理任务未独立持久化：%+v %v", items, err)
	}
	item := items[0]
	if item.ScopeType != repository.ScopeSystem || item.ProjectID != "" || item.OperatorID != "" {
		t.Fatalf("管理任务没有保持系统范围：%+v", item)
	}
	if item.Status != status || item.FinishedAt == nil || item.TaskType != "management_renewal" {
		t.Fatalf("管理任务终态异常：%+v", item)
	}
	trials, err := f.app.Tasks.ListAttempts(t.Context(), item.ID)
	if err != nil || len(trials) != attempts {
		t.Fatalf("管理任务尝试异常：%+v %v", trials, err)
	}
	for _, trial := range trials {
		if trial.Status == "running" || trial.FinishedAt == nil {
			t.Fatalf("尝试未收尾：%+v", trial)
		}
	}
	var count int
	if err := f.app.Database.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs WHERE action = ? AND status = ?", "management_renewal", status).Scan(&count); err != nil || count != 1 {
		t.Fatalf("管理续期审计缺失：%d %v", count, err)
	}
	if err := f.app.Database.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs WHERE action = 'management_renewal' AND (task_id <> ? OR scope_type <> 'system' OR project_id <> '' OR operator_id <> '')", item.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("管理续期审计范围或关联错误：%d，%v", count, err)
	}
	return item
}

func TestManagementRenewalPersistsWithoutBusinessDomain(t *testing.T) {
	f := newManagementFixture(t)
	report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
	if err != nil || report.Checked != 1 || report.Selected != 1 || report.Succeeded != 1 || f.issues != 1 || f.activator.calls != 1 {
		t.Fatalf("管理续期未完成：%+v %v", report, err)
	}
	task := f.assertTask(t, "success", 1)
	var count int
	if err := f.app.Database.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs WHERE task_id = ? AND status IN ('running', 'success')", task.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("正常管理续期缺少开始或收尾审计：%d，%v", count, err)
	}
	items, err := f.app.Repository.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("管理续期不得创建业务域名：%+v %v", items, err)
	}
	if len(f.notifier.events) != 1 || f.notifier.events[0].Domain != f.state.Hostname || f.notifier.events[0].CertificateSerial != "123" || f.notifier.events[0].ManagementURL != f.state.ManagementURL {
		t.Fatalf("管理续期通知字段不完整：%+v", f.notifier.events)
	}
}

func TestManagementRenewalSkipsFreshAndIPCertificates(t *testing.T) {
	for _, name := range []string{"fresh", "ip", "near"} {
		t.Run(name, func(t *testing.T) {
			f := newManagementFixture(t)
			f.state.NotAfter = f.now.Add(40 * 24 * time.Hour)
			if name == "ip" {
				f.state.Hostname = ""
			}
			if name == "near" {
				f.state.NotAfter = f.now.Add(8 * 24 * time.Hour)
			}
			report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
			if err != nil || report.Selected != 0 || f.issues != 0 || f.activator.calls != 0 || len(f.notifier.events) != 0 {
				t.Fatalf("不应执行签发：%+v %v", report, err)
			}
			if name == "near" && report.NearExpiry != 1 {
				t.Fatal("临期证书未纳入每日检查")
			}
			items, err := f.app.Tasks.ListTasks(t.Context(), "", 10)
			if err != nil || len(items) != 0 {
				t.Fatalf("跳过时不应产生任务：%v", err)
			}
		})
	}
}

func TestManagementRenewalPreflightRejectsBeforeIssuance(t *testing.T) {
	for _, name := range []string{"dns", "proxy", "no_listener"} {
		t.Run(name, func(t *testing.T) {
			f := newManagementFixture(t)
			if name == "dns" {
				f.state.DNSVerified = false
			} else {
				if name == "proxy" {
					f.state.HTTPSMode = "proxy"
				}
				f.m.activator = directManagementActivator{server: f.app.HTTP}
			}
			report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
			if err == nil || report.Failed != 1 || f.issues != 0 || f.activator.calls != 0 {
				t.Fatalf("前置检查失败仍执行了签发：%+v %v", report, err)
			}
			f.assertTask(t, "failed", 0)
		})
	}
}

func TestManagementRenewalRetriesOnlyIssuance(t *testing.T) {
	for _, name := range []string{"recover", "exhaust", "activation", "shorter"} {
		t.Run(name, func(t *testing.T) {
			f := newManagementFixture(t)
			f.state.NotAfter = f.now.Add(2 * 24 * time.Hour)
			baseIssue := f.m.issue
			calls := 0
			f.m.issue = func(ctx context.Context, request certificate.SetupCertificateRequest) (certificate.SavedResource, error) {
				calls++
				if name == "exhaust" || (name == "recover" && calls < 3) {
					return certificate.SavedResource{}, errors.New("测试凭据不得出现在通知中")
				}
				saved, err := baseIssue(ctx, request)
				if name == "shorter" {
					saved.NotAfter = f.state.NotAfter
				}
				return saved, err
			}
			if name == "activation" {
				f.activator.activateErr = errors.New("模拟代理激活失败")
			}
			report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
			wantAttempts, status := 1, "failed"
			if name == "recover" || name == "exhaust" {
				wantAttempts = 3
			}
			if name == "recover" {
				status = "success"
			}
			if calls != wantAttempts || (err == nil) != (status == "success") || report.Urgent != 1 {
				t.Fatalf("重试范围错误：calls=%d report=%+v err=%v", calls, report, err)
			}
			f.assertTask(t, status, wantAttempts)
			if len(f.notifier.events) != 1 || f.notifier.events[0].Status != status || strings.Contains(f.notifier.events[0].Error, "测试凭据") {
				t.Fatalf("通知状态错误或泄露错误细节：%+v", f.notifier.events)
			}
		})
	}
}

func TestManagementRenewalCancellationPersistsTerminalState(t *testing.T) {
	for _, name := range []string{"issue", "retry_wait"} {
		t.Run(name, func(t *testing.T) {
			f := newManagementFixture(t)
			f.state.NotAfter = f.now.Add(time.Hour)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.m.issue = func(context.Context, certificate.SetupCertificateRequest) (certificate.SavedResource, error) {
				f.issues++
				if name == "issue" {
					cancel()
					return certificate.SavedResource{}, ctx.Err()
				}
				return certificate.SavedResource{}, errors.New("模拟签发失败")
			}
			f.m.wait = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
			report, err := f.m.RunRenewal(ctx, f.policy, f.now)
			if !errors.Is(err, context.Canceled) || report.Failed != 1 || f.issues != 1 || f.activator.calls != 0 {
				t.Fatalf("取消后仍继续执行：%+v %v", report, err)
			}
			f.assertTask(t, "cancelled", 1)
		})
	}
}

type managementFailTasks struct {
	repository.TaskRepository
	failure string
}

func (r managementFailTasks) CreateTask(ctx context.Context, item repository.Task) error {
	if r.failure == "create" {
		return errors.New("模拟任务写入失败")
	}
	return r.TaskRepository.CreateTask(ctx, item)
}

func (r managementFailTasks) CreateAttempt(ctx context.Context, item repository.TaskAttempt) error {
	if r.failure == "attempt" {
		return errors.New("模拟尝试写入失败")
	}
	return r.TaskRepository.CreateAttempt(ctx, item)
}

func TestManagementRenewalDoesNotIssueWithoutPersistentTask(t *testing.T) {
	for _, failure := range []string{"create", "attempt"} {
		t.Run(failure, func(t *testing.T) {
			f := newManagementFixture(t)
			f.m.tasks = managementFailTasks{TaskRepository: f.app.Tasks, failure: failure}
			report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
			if err == nil || report.Failed != 1 || f.issues != 0 || f.activator.calls != 0 {
				t.Fatalf("无任务记录仍签发：%+v %v", report, err)
			}
			if failure == "attempt" {
				f.assertTask(t, "failed", 0)
			}
		})
	}
}

func TestManagementRenewalRejectsConcurrentRuns(t *testing.T) {
	f := newManagementFixture(t)
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if _, err := f.m.RunRenewal(t.Context(), f.policy, f.now); err == nil || f.issues != 0 {
		t.Fatal("未拒绝重复续期")
	}
}

func TestManagementRenewalNotificationFailureDoesNotRepeatIssuance(t *testing.T) {
	f := newManagementFixture(t)
	f.notifier.err = errors.New("模拟邮件发送失败")
	logged := false
	f.m.notifyError = func(error) { logged = true }
	report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
	if err != nil || report.Succeeded != 1 || !logged || f.issues != 1 {
		t.Fatalf("通知失败不应重复签发：%+v %v", report, err)
	}
	f.assertTask(t, "success", 1)
}

func TestManagementRenewalRecordsUnreadableCertificate(t *testing.T) {
	f := newManagementFixture(t)
	f.m.readState = func() (httpapi.ManagementCertificateState, error) {
		return f.state, errors.New("模拟管理证书文件读取失败")
	}
	logged := false
	f.m.notifyError = func(error) { logged = true }
	report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
	if err == nil || report.Checked != 1 || report.Failed != 1 || !logged || f.issues != 0 {
		t.Fatalf("读取错误没有持久化失败证据：%+v %v", report, err)
	}
	f.assertTask(t, "failed", 0)
	if len(f.notifier.events) != 1 || f.notifier.events[0].Status != "failed" {
		t.Fatal("证书读取失败没有通知")
	}
}

func TestManagementRenewalRecordsUnreadableEmptyState(t *testing.T) {
	f := newManagementFixture(t)
	f.m.readState = func() (httpapi.ManagementCertificateState, error) {
		return httpapi.ManagementCertificateState{}, errors.New("empty management certificate state read failure")
	}
	report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
	if err == nil || report.Checked != 1 || report.Failed != 1 || f.issues != 0 {
		t.Fatalf("empty state read failure was incorrectly skipped: %+v %v", report, err)
	}
	f.assertTask(t, "failed", 0)
}

type managementFailAudit struct {
	repository.AuditRepository
}

func (r managementFailAudit) CreateAuditLog(ctx context.Context, item repository.AuditLog) error {
	if item.Status == "running" {
		return errors.New("模拟审计写入失败")
	}
	return r.AuditRepository.CreateAuditLog(ctx, item)
}

func TestManagementRenewalRejectsBeforeSideEffectsOnAuditFailure(t *testing.T) {
	f := newManagementFixture(t)
	f.m.audit = managementFailAudit{AuditRepository: f.m.audit}
	report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
	if err == nil || report.Failed != 1 || f.issues != 0 || f.activator.calls != 0 {
		t.Fatalf("审计失败后仍签发：%+v %v", report, err)
	}
	f.assertTask(t, "failed", 0)
}

type managementFailFinish struct {
	repository.TaskRepository
}

func (r managementFailFinish) FinishAttempt(context.Context, string, string, string, *time.Time) error {
	return errors.New("模拟尝试终态写入失败")
}

func TestManagementRenewalReportsPersistenceFailureWithoutAnotherIssue(t *testing.T) {
	f := newManagementFixture(t)
	f.state.NotAfter = f.now.Add(time.Hour)
	f.m.tasks = managementFailFinish{TaskRepository: f.app.Tasks}
	report, err := f.m.RunRenewal(t.Context(), f.policy, f.now)
	if err == nil || report.Succeeded != 0 || report.Failed != 1 || f.issues != 1 || f.activator.calls != 1 {
		t.Fatalf("记录失败被误报为成功或重复签发：%+v %v", report, err)
	}
	items, err := f.app.Tasks.ListTasks(t.Context(), managementResourceID, 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" || items[0].Stage != "record" {
		t.Fatalf("持久化失败没有失败终态：%+v %v", items, err)
	}
}

func TestApplicationWiresManagementRenewalEvenWithEmptyBusinessList(t *testing.T) {
	cfg := readyApplicationConfig(t)
	a, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	// 此配置没有管理证书文件，应记录读取失败，不能假装空列表全部成功。
	report, err := a.Renewal.RunOnce(t.Context())
	if err == nil || report.Checked != 1 || report.Failed != 1 {
		t.Fatalf("应用仍未接入管理续期：%+v %v", report, err)
	}
	items, err := a.Tasks.ListTasks(t.Context(), managementResourceID, 10)
	if err != nil || len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("应用管理续期没有任务：%+v %v", items, err)
	}
}
