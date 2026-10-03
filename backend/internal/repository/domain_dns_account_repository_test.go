package repository

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

func TestDomainDNSAccountRepository(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.Default(t.TempDir()).Database
			cfg.DSN = filepath.Join(t.TempDir(), "domain-dns.db")
			var admin *sql.DB
			if driver == "mysql" {
				server := mysqltest.New(t)
				cfg, admin = server.Database(t), server.Admin()
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
			checkDomainDNSAccountRepository(t, db.DB, admin, driver)
		})
	}
}

func checkDomainDNSAccountRepository(t *testing.T, db, admin *sql.DB, driver string) {
	t.Helper()
	repo := NewSQLProjectMemberRepository(db, driver)
	domains := NewSQLDomainRepository(db, driver)
	for _, actor := range []struct{ id, role string }{{"root", "super_admin"}, {"manager", "user"}, {"cert", "user"}, {"dns", "user"}, {"reader", "user"}, {"outsider", "admin"}} {
		if _, err := db.ExecContext(t.Context(), Rebind(driver, `INSERT INTO users (id, username, password_hash, role, status) VALUES (?, ?, 'test-only', ?, 'active')`), actor.id, actor.id, actor.role); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"binding-a", "binding-b"} {
		if err := NewSQLProjectRepository(db, driver).CreateProject(t.Context(), domain.Project{ID: id, Name: "绑定测试"}); err != nil {
			t.Fatal(err)
		}
	}
	for actor, role := range map[string]domain.ProjectRole{"manager": domain.ProjectRoleAdmin, "cert": domain.ProjectRoleCertificateAdmin, "dns": domain.ProjectRoleDNSAdmin, "reader": domain.ProjectRoleReadOnly} {
		if err := repo.GrantProjectRole(t.Context(), "root", "binding-a", actor, role); err != nil {
			t.Fatal(err)
		}
	}
	makeDomain := func(id string) domain.Domain {
		t.Helper()
		item, err := domain.NewDomain(id+".example.test", domain.CertificateSingle, nil)
		if err != nil {
			t.Fatal(err)
		}
		item.ID, item.ProjectID = id, "binding-a"
		if err := repo.CreateProjectDomain(t.Context(), "root", item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	item := makeDomain("bound-domain")
	second := makeDomain("second-domain")
	accounts := make(map[string]domain.DNSAccount)
	for _, id := range []string{"account-a", "account-b", "foreign-account"} {
		projectID := "binding-a"
		if id == "foreign-account" {
			projectID = "binding-b"
		}
		account, err := repo.CreateDNSAccount(t.Context(), "root", domain.DNSAccount{ID: id, Name: "测试账户", ProjectID: projectID, Provider: "cloudflare", CredentialRef: "secret://dns/" + id})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, id, "", account.Revision); err == nil {
			t.Fatal("未验证账户不应允许绑定")
		}
		account, err = repo.RecordDNSAccountVerification(t.Context(), "root", id, account.Revision, true)
		if err != nil {
			t.Fatal(err)
		}
		accounts[id] = account
	}
	a := accounts["account-a"]
	httpItem := makeDomain("http-domain")
	httpItem.ChallengeType = domain.ChallengeHTTP01
	if err := repo.UpdateProjectDomain(t.Context(), "root", httpItem); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", httpItem.ID, a.ID, "", a.Revision); !errors.Is(err, ErrDNSBindingInvalid) {
		t.Fatal("HTTP 验证域名不应绑定 DNS 账户", err)
	}
	disabled, err := repo.SetDNSAccountEnabled(t.Context(), "root", "account-b", accounts["account-b"].Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, disabled.ID, "", disabled.Revision); !errors.Is(err, ErrDNSBindingInvalid) {
		t.Fatal("禁用账户不应允许新绑定", err)
	}
	for _, actor := range []string{"", "missing", "cert", "dns", "reader", "outsider"} {
		if _, err := repo.SetDomainDNSAccountBinding(t.Context(), actor, item.ID, a.ID, "", a.Revision); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("角色 %s 越权绑定未拒绝：%v", actor, err)
		}
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, "foreign-account", "", 2); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("超级管理员也不能跨项目绑定", err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "manager", item.ID, a.ID, "", a.Revision-1); !errors.Is(err, ErrDNSAccountConflict) {
		t.Fatal("旧凭据版本未拒绝", err)
	}
	binding, err := repo.SetDomainDNSAccountBinding(t.Context(), "manager", item.ID, a.ID, "", a.Revision)
	if err != nil || binding.ID == "" || binding.ProjectID != item.ProjectID || binding.DNSAccountID != a.ID {
		t.Fatal("绑定失败", binding, err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", second.ID, a.ID, "", a.Revision); err != nil {
		t.Fatal("同一账户应允许绑定多个域名", err)
	}
	for _, read := range []func() (domain.Domain, error){
		func() (domain.Domain, error) { return domains.GetByID(t.Context(), item.ID) },
		func() (domain.Domain, error) { return repo.GetVisibleDomain(t.Context(), "cert", item.ID) },
	} {
		stored, err := read()
		if err != nil || stored.DNSAccountID != a.ID || stored.DNSBindingID != binding.ID || stored.DNSProvider != a.Provider {
			t.Fatal("域名查询遗漏真实绑定", stored, err)
		}
	}
	if read, err := repo.GetDomainDNSAccountBinding(t.Context(), "reader", item.ID); err != nil || read.ID != binding.ID {
		t.Fatal("同项目只读账户应能查看绑定元数据", err)
	}
	if _, err := repo.GetDomainDNSAccountBinding(t.Context(), "outsider", item.ID); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("外项目不应查看绑定", err)
	}
	item.DNSProvider = a.Provider
	item.ChallengeType = domain.ChallengeHTTP01
	if err := repo.UpdateProjectDomain(t.Context(), "manager", item); !errors.Is(err, ErrDNSBindingInvalid) {
		t.Fatal("普通编辑绕过绑定切换验证方式", err)
	}
	if err := domains.Update(t.Context(), item); !errors.Is(err, ErrDNSBindingInvalid) {
		t.Fatal("基础仓储绕过绑定切换验证方式", err)
	}
	item.ChallengeType = domain.ChallengeDNS01
	item.DNSProvider = "aliyun"
	if err := repo.UpdateProjectDomain(t.Context(), "manager", item); !errors.Is(err, ErrDNSBindingInvalid) {
		t.Fatal("普通编辑绕过绑定切换服务商", err)
	}
	item.DNSProvider = a.Provider
	if err := repo.UpdateProjectDomain(t.Context(), "manager", item); err != nil {
		t.Fatal("合法编辑不能破坏绑定", err)
	}
	for _, status := range []string{"queued", "running", "retry_wait", "cancelling"} {
		if _, err := db.ExecContext(t.Context(), Rebind(driver, `INSERT INTO tasks (id,domain_id,task_type,status,message) VALUES ('binding-task',?,'certificate_issue',?,'')`), item.ID, status); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, "", binding.ID, 0); !errors.Is(err, ErrDNSBindingBusy) {
			t.Fatal("活动任务未阻止解除绑定", status, err)
		}
		if _, err := db.ExecContext(t.Context(), `DELETE FROM tasks WHERE id='binding-task'`); err != nil {
			t.Fatal(err)
		}
	}
	// 同一个旧绑定令牌只能有一个并发写入成功；数据库忙或冲突均不能绕过条件重试。
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, a.ID, binding.ID, a.Revision)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("同一绑定条件应只有一个成功者", winners)
	}
	current, err := repo.GetDomainDNSAccountBinding(t.Context(), "root", item.ID)
	if err != nil || current.ID == binding.ID {
		t.Fatal("绑定令牌未变化", err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, "", binding.ID, 0); !errors.Is(err, ErrDNSBindingConflict) {
		t.Fatal("旧绑定仍可解除", err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "cert", item.ID, "", current.ID, 0); err != nil {
		t.Fatal("证书管理员无法显式解除绑定", err)
	}
	rebound, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, a.ID, "", a.Revision)
	if err != nil || rebound.ID == current.ID || rebound.ID == binding.ID {
		t.Fatal("解除后重绑重复使用旧令牌", err)
	}
	if _, err := db.ExecContext(t.Context(), Rebind(driver, `DELETE FROM dns_accounts WHERE id=?`), a.ID); err == nil {
		t.Fatal("引用账户被直接删除")
	}
	if err := repo.DeleteProjectDomain(t.Context(), "root", second.ID); err != nil {
		t.Fatal("删除域名没有同时解除引用", err)
	}
	if _, err := repo.GetVisibleDNSAccount(t.Context(), "root", a.ID); err != nil {
		t.Fatal("删除域名误删账户", err)
	}
	if err := repo.RevokeProjectRole(t.Context(), "root", "binding-a", "manager", domain.ProjectRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "manager", item.ID, "", rebound.ID, 0); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatal("撤权后仍可修改绑定", err)
	}
	// 故障注入只对隔离库使用管理连接，不扩大应用账户权限。
	executor := db
	statements := []string{`CREATE TRIGGER reject_binding_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'blocked'); END`}
	if driver == "mysql" {
		var schema string
		if err := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		schema = "`" + strings.ReplaceAll(schema, "`", "``") + "`"
		executor = admin
		statements = []string{`CREATE TRIGGER ` + schema + `.reject_binding_audit BEFORE INSERT ON ` + schema + `.audit_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='blocked'`}
	} else if driver == "postgres" {
		statements = []string{`CREATE FUNCTION reject_binding_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked'; END $$`, `CREATE TRIGGER reject_binding_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION reject_binding_audit()`}
	}
	for _, statement := range statements {
		if _, err := executor.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "root", item.ID, "", rebound.ID, 0); err == nil {
		t.Fatal("审计失败仍修改绑定")
	}
	if err := repo.DeleteProjectDomain(t.Context(), "root", item.ID); err == nil {
		t.Fatal("审计失败仍删除域名")
	}
	if stored, err := repo.GetDomainDNSAccountBinding(t.Context(), "root", item.ID); err != nil || stored.ID != rebound.ID {
		t.Fatal("审计失败未恢复绑定", err)
	}
}
