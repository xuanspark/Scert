package repository

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

func TestDNSAccountRepository(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.Default(t.TempDir()).Database
			cfg.DSN = filepath.Join(t.TempDir(), "dns-accounts.db")
			var admin *sql.DB
			if driver == "mysql" {
				server := mysqltest.New(t)
				cfg = server.Database(t)
				admin = server.Admin()
			}
			if driver == "postgres" {
				cfg = postgrestest.New(t).Database(t)
			}
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := database.Migrate(t.Context(), db.DB, driver); err != nil {
				t.Fatal(err)
			}
			checkDNSAccountRepository(t, db.DB, admin, driver)
		})
	}
}

func TestImportLegacyDNSAccountIsIdempotentAndPreservesExistingChanges(t *testing.T) {
	cfg := config.Default(t.TempDir()).Database
	cfg.DSN = filepath.Join(t.TempDir(), "legacy-dns.db")
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLProjectMemberRepository(db.DB, "sqlite")
	item := domain.DNSAccount{
		ID: "legacy-system-dns", Name: "旧系统 DNS 账户", ProjectID: domain.DefaultProjectID,
		OwnerUserID: "primary-super-admin", Provider: "aliyun", CredentialRef: "secret://dns/legacy",
	}
	if err := repo.ImportLegacyDNSAccount(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	var name, status, verification, reference string
	var revision int
	if err := db.DB.QueryRowContext(t.Context(), `SELECT name,status,verification_state,credential_ref,revision FROM dns_accounts WHERE id = ?`, item.ID).
		Scan(&name, &status, &verification, &reference, &revision); err != nil {
		t.Fatal(err)
	}
	if name != item.Name || status != domain.DNSAccountActive || verification != domain.DNSAccountUnverified || reference != item.CredentialRef || revision != 1 {
		t.Fatalf("旧账户初次导入状态错误：%s %s %s %s %d", name, status, verification, reference, revision)
	}
	if _, err := db.DB.ExecContext(t.Context(), `UPDATE dns_accounts SET name = ?, status = ?, verification_state = ?, revision = ? WHERE id = ?`, "用户修改后的账户", domain.DNSAccountDisabled, domain.DNSAccountVerified, 8, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.ImportLegacyDNSAccount(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.QueryRowContext(t.Context(), `SELECT name,status,verification_state,revision FROM dns_accounts WHERE id = ?`, item.ID).
		Scan(&name, &status, &verification, &revision); err != nil {
		t.Fatal(err)
	}
	if name != "用户修改后的账户" || status != domain.DNSAccountDisabled || verification != domain.DNSAccountVerified || revision != 8 {
		t.Fatal("重复导入覆盖了现有账户修改")
	}
	conflict := item
	conflict.CredentialRef = "secret://dns/other"
	if err := repo.ImportLegacyDNSAccount(t.Context(), conflict); !errors.Is(err, ErrLegacyDNSAccountConflict) {
		t.Fatalf("固定旧账户标识冲突未拒绝：%v", err)
	}
}

func checkDNSAccountRepository(t *testing.T, db, admin *sql.DB, driver string) {
	t.Helper()
	repo := NewSQLProjectMemberRepository(db, driver)
	for _, actor := range []struct{ id, role string }{
		{"root", "super_admin"}, {"manager", "user"}, {"dns", "user"}, {"reader", "user"},
		{"cert", "user"}, {"audit", "user"}, {"outsider", "admin"},
	} {
		if _, err := db.ExecContext(t.Context(), Rebind(driver, `INSERT INTO users (id,username,password_hash,role,status)
			VALUES (?,?,'test-only-no-login',?,'active')`), actor.id, actor.id, actor.role); err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range []string{"dns-a", "dns-b"} {
		if err := NewSQLProjectRepository(db, driver).CreateProject(t.Context(), domain.Project{ID: project, Name: "DNS 测试项目"}); err != nil {
			t.Fatal(err)
		}
	}
	for actor, role := range map[string]domain.ProjectRole{
		"manager": domain.ProjectRoleAdmin, "dns": domain.ProjectRoleDNSAdmin, "reader": domain.ProjectRoleReadOnly,
		"cert": domain.ProjectRoleCertificateAdmin, "audit": domain.ProjectRoleAuditViewer,
	} {
		if err := repo.GrantProjectRole(t.Context(), "root", "dns-a", actor, role); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.GrantProjectRole(t.Context(), "root", "dns-a", "manager", domain.ProjectRoleReadOnly); err != nil {
		t.Fatal(err)
	}
	makeAccount := func(id, project string) domain.DNSAccount {
		return domain.DNSAccount{ID: id, Name: "账户" + id, ProjectID: project, Provider: "aliyun", CredentialRef: "secret://dns/aliyun/" + id,
			OwnerUserID: "forged-owner", VerificationState: domain.DNSAccountVerified, Revision: 999}
	}
	a, err := repo.CreateDNSAccount(t.Context(), "dns", makeAccount("account-a", "dns-a"))
	if err != nil || a.OwnerUserID != "dns" || a.Revision != 1 || a.VerificationState != domain.DNSAccountUnverified || a.CredentialRef != "" {
		t.Fatal("创建未固定真实归属或初始状态", a, err)
	}
	if _, err := repo.CreateDNSAccount(t.Context(), "root", makeAccount("account-b", "dns-b")); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"reader", "cert", "audit", "outsider", "missing"} {
		if _, err := repo.CreateDNSAccount(t.Context(), actor, makeAccount("denied", "dns-a")); err == nil {
			t.Fatalf("越权创建未拒绝：%s", actor)
		}
	}
	for actor, count := range map[string]int{"root": 2, "manager": 1, "dns": 1, "reader": 1, "cert": 0, "audit": 0, "outsider": 0, "missing": 0} {
		items, err := repo.ListVisibleDNSAccounts(t.Context(), actor)
		if err != nil || len(items) != count {
			t.Fatalf("账户可见范围错误：%s %d %v", actor, len(items), err)
		}
		for _, item := range items {
			if item.CredentialRef != "" {
				t.Fatal("列表返回凭据引用")
			}
		}
	}
	for _, id := range []string{"account-b", "missing"} {
		if _, err := repo.GetVisibleDNSAccount(t.Context(), "dns", id); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatal("跨项目和不存在账户应同样拒绝", err)
		}
	}
	if _, err := repo.GetDNSAccountForUse(t.Context(), "dns", a.ID, "dns-a"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("未验证账户可被使用", err)
	}
	for _, actor := range []string{"root", "manager", "dns"} {
		item, err := repo.GetDNSAccountForVerification(t.Context(), actor, a.ID)
		if err != nil || item.CredentialRef == "" || item.Revision != a.Revision {
			t.Fatal("管理角色无法开始首次验证", actor, err)
		}
	}
	for _, actor := range []string{"reader", "cert", "audit", "outsider", "missing"} {
		if _, err := repo.GetDNSAccountForVerification(t.Context(), actor, a.ID); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatal("无管理权限取得待验证凭据", actor, err)
		}
	}
	a, err = repo.RecordDNSAccountVerification(t.Context(), "dns", a.ID, a.Revision, true)
	if err != nil || a.Revision != 2 {
		t.Fatal(err)
	}
	used, err := repo.GetDNSAccountForUse(t.Context(), "dns", a.ID, "dns-a")
	if err != nil || used.CredentialRef != "secret://dns/aliyun/account-a" {
		t.Fatal("已授权使用未取得正确引用", err)
	}
	if _, err := repo.GetDNSAccountForUse(t.Context(), "root", a.ID, "dns-b"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("超级管理员也不得跨项目使用", err)
	}
	if _, err := repo.GetDNSAccountForUse(t.Context(), "reader", a.ID, "dns-a"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("只读角色取得凭据引用", err)
	}
	if _, err := repo.RenameDNSAccount(t.Context(), "outsider", a.ID, a.Revision, "越权"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal(err)
	}
	old := a.Revision
	a, err = repo.RotateDNSAccountCredential(t.Context(), "dns", a.ID, old, "secret://dns/aliyun/rotated")
	if err != nil || a.VerificationState != domain.DNSAccountUnverified {
		t.Fatal("轮换未使验证失效", err)
	}
	if _, err := repo.RecordDNSAccountVerification(t.Context(), "dns", a.ID, old, true); !errors.Is(err, ErrDNSAccountConflict) {
		t.Fatal("旧验证覆盖轮换结果", err)
	}
	if _, err := repo.RotateDNSAccountCredential(t.Context(), "dns", a.ID, a.Revision, "secret://dns/aliyun/rotated"); err == nil {
		t.Fatal("复用旧引用轮换未被拒绝")
	}
	if _, err := repo.RotateDNSAccountCredential(t.Context(), "dns", a.ID, a.Revision, "secret://dns/aliyun/account-b"); err == nil || strings.Contains(err.Error(), "secret://") {
		t.Fatal("重复引用未拒绝或泄露秘密位置", err)
	}
	old = a.Revision
	a, err = repo.SetDNSAccountEnabled(t.Context(), "manager", a.ID, old, false)
	if err != nil || a.Status != domain.DNSAccountDisabled {
		t.Fatal(err)
	}
	if _, err := repo.RecordDNSAccountVerification(t.Context(), "dns", a.ID, old, true); !errors.Is(err, ErrDNSAccountConflict) {
		t.Fatal("旧验证覆盖禁用状态", err)
	}
	if _, err := repo.RecordDNSAccountVerification(t.Context(), "dns", a.ID, a.Revision, true); err == nil {
		t.Fatal("禁用账户允许完成验证")
	}
	if _, err := repo.GetDNSAccountForUse(t.Context(), "root", a.ID, "dns-a"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("禁用账户允许使用", err)
	}
	if _, err := repo.GetDNSAccountForVerification(t.Context(), "root", a.ID); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("禁用账户可开始验证", err)
	}
	a, err = repo.SetDNSAccountEnabled(t.Context(), "manager", a.ID, a.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	a, err = repo.RecordDNSAccountVerification(t.Context(), "dns", a.ID, a.Revision, false)
	if err != nil || a.VerificationState != domain.DNSAccountVerificationFailed {
		t.Fatal(err)
	}
	if _, err := repo.GetDNSAccountForUse(t.Context(), "dns", a.ID, "dns-a"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("验证失败账户可使用", err)
	}
	a, err = repo.RenameDNSAccount(t.Context(), "manager", a.ID, a.Revision, "轮换后的账户")
	if err != nil || a.OwnerUserID != "dns" || a.ProjectID != "dns-a" || a.Provider != "aliyun" {
		t.Fatal("修改时归属发生变化", err)
	}
	if err := repo.RevokeProjectRole(t.Context(), "root", "dns-a", "dns", domain.ProjectRoleDNSAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordDNSAccountVerification(t.Context(), "dns", a.ID, a.Revision, true); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("撤权后验证结果仍可写入", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE users SET status='disabled' WHERE id='manager'`); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListVisibleDNSAccounts(t.Context(), "manager")
	if err != nil || len(items) != 0 {
		t.Fatal("停用账户仍可读取", err)
	}
	if _, err := repo.RenameDNSAccount(t.Context(), "manager", a.ID, a.Revision, "已停用"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("停用账户仍可修改", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_logs WHERE resource_type='dns_account' AND (message LIKE '%secret://%' OR message LIKE '%rotated%')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("审计包含秘密引用", err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_logs WHERE resource_type='dns_account' AND resource_id='account-a'`).Scan(&count); err != nil || int64(count) != a.Revision {
		t.Fatal("成功修订与事务审计数量不一致", count, a.Revision, err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM projects WHERE id='dns-b'`); err == nil {
		t.Fatal("删除项目产生孤立 DNS 账户")
	}
	concurrent, err := repo.CreateDNSAccount(t.Context(), "root", makeAccount("concurrent", "dns-a"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := repo.RenameDNSAccount(t.Context(), "root", concurrent.ID, concurrent.Revision, "并发改名")
		results <- err
	}()
	go func() {
		<-start
		_, err := repo.SetDNSAccountEnabled(t.Context(), "root", concurrent.ID, concurrent.Revision, false)
		results <- err
	}()
	close(start)
	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrDNSAccountConflict) && !(driver == "sqlite" && strings.Contains(strings.ToLower(err.Error()), "locked")) {
			t.Fatal("并发修改出现非预期错误", err)
		}
	}
	stored, err := repo.GetVisibleDNSAccount(t.Context(), "root", concurrent.ID)
	if err != nil || successes != 1 || stored.Revision != 2 {
		t.Fatal("并发更新没有唯一胜出版本", successes, stored.Revision, err)
	}
	var auditCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_logs WHERE resource_type='dns_account' AND resource_id='concurrent'`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatal("并发失败留下多余审计", err)
	}
	var rejection []string
	executor := db
	switch driver {
	case "sqlite":
		rejection = []string{`CREATE TRIGGER reject_dns_audit BEFORE INSERT ON audit_logs WHEN NEW.resource_type='dns_account' BEGIN SELECT RAISE(ABORT, 'blocked'); END`}
	case "mysql":
		// 仅隔离库故障注入使用管理连接，业务仓储仍使用受限账户，不放宽全局权限。
		var schema string
		if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
		rejection = []string{`CREATE TRIGGER ` + schema + `.reject_dns_audit BEFORE INSERT ON ` + schema + `.audit_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='blocked'`}
		executor = admin
	case "postgres":
		rejection = []string{`CREATE FUNCTION reject_dns_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked'; END $$`,
			`CREATE TRIGGER reject_dns_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION reject_dns_audit()`}
	}
	for _, statement := range rejection {
		if _, err := executor.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.RenameDNSAccount(t.Context(), "root", a.ID, a.Revision, "不应写入"); err == nil {
		t.Fatal("审计失败未阻止变更")
	}
	after, err := repo.GetVisibleDNSAccount(t.Context(), "root", a.ID)
	if err != nil || after.Name != a.Name || after.Revision != a.Revision {
		t.Fatal("审计失败未回滚", err)
	}
	if _, err := repo.CreateDNSAccount(t.Context(), "root", makeAccount("failed-audit", "dns-a")); err == nil {
		t.Fatal("审计失败仍创建账户")
	}
	if _, err := repo.GetVisibleDNSAccount(t.Context(), "root", "failed-audit"); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("创建失败留下账户", err)
	}
}
