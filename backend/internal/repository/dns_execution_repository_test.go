package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

type dnsLeaseFixture struct {
	repo     *SQLProjectMemberRepository
	db       *sql.DB
	driver   string
	item     domain.Domain
	account  domain.DNSAccount
	identity domain.DNSExecutionIdentity
}

func (f dnsLeaseFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), Rebind(f.driver, query), args...); err != nil {
		t.Fatal(err)
	}
}

func newDNSLeaseFixture(t *testing.T, db *sql.DB, driver string) dnsLeaseFixture {
	t.Helper()
	repo := NewSQLProjectMemberRepository(db, driver)
	item, err := domain.NewDomain(domain.NewID()+".example.test", domain.CertificateSingle, nil)
	if err != nil {
		t.Fatal(err)
	}
	item.ProjectID = domain.NewID()
	if err := NewSQLProjectRepository(db, driver).CreateProject(t.Context(), domain.Project{ID: item.ProjectID, Name: "租约测试"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.GrantProjectRole(t.Context(), "lease-root", item.ProjectID, "lease-manager", domain.ProjectRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProjectDomain(t.Context(), "lease-root", item); err != nil {
		t.Fatal(err)
	}
	account, err := repo.CreateDNSAccount(t.Context(), "lease-root", domain.DNSAccount{ID: domain.NewID(), Name: "租约账户", ProjectID: item.ProjectID, Provider: "cloudflare", CredentialRef: "secret://dns/" + domain.NewID()})
	if err != nil {
		t.Fatal(err)
	}
	account, err = repo.RecordDNSAccountVerification(t.Context(), "lease-root", account.ID, account.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetDomainDNSAccountBinding(t.Context(), "lease-root", item.ID, account.ID, "", account.Revision); err != nil {
		t.Fatal(err)
	}
	item, err = NewSQLDomainRepository(db, driver).GetByID(t.Context(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.DNSExecutionIdentity{TaskID: domain.NewID(), AttemptID: domain.NewID(), AttemptNumber: 1}
	tasks := NewSQLTaskRepository(db, driver)
	if err := tasks.CreateTask(t.Context(), Task{ID: identity.TaskID, DomainID: item.ID, TaskType: "certificate_issue", Status: "running", Stage: "certificate_issue", OperatorID: "lease-manager"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateAttempt(t.Context(), TaskAttempt{ID: identity.AttemptID, TaskID: identity.TaskID, AttemptNumber: 1, Status: "running", Stage: "certificate_issue"}); err != nil {
		t.Fatal(err)
	}
	return dnsLeaseFixture{repo: repo, db: db, driver: driver, item: item, account: account, identity: identity}
}

func TestDNSExecutionLeaseRepository(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.Default(t.TempDir()).Database
			cfg.DSN = filepath.Join(t.TempDir(), "dns-leases.db")
			if driver == "mysql" {
				cfg = mysqltest.New(t).Database(t)
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
			for id, role := range map[string]string{"lease-root": "super_admin", "lease-manager": "user"} {
				if _, err := db.DB.ExecContext(t.Context(), Rebind(driver, `INSERT INTO users (id, username, password_hash, role, status) VALUES (?, ?, 'test-only', ?, 'active')`), id, id, role); err != nil {
					t.Fatal(err)
				}
			}
			t.Run("lifecycle", func(t *testing.T) {
				f := newDNSLeaseFixture(t, db.DB, driver)
				lease, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity)
				if err != nil {
					t.Fatal(err)
				}
				if lease.Provider != "cloudflare" || lease.AccountID != f.account.ID || lease.CredentialRef == "" || lease.Revision != f.account.Revision {
					t.Fatal("租约未固定实际账户")
				}
				if err := f.repo.ValidateDNSExecutionLease(t.Context(), lease); err != nil {
					t.Fatal(err)
				}
				if _, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity); err == nil {
					t.Fatal("重复获取未拒绝")
				}
				for _, mutate := range []func() error{
					func() error {
						_, err := f.repo.RotateDNSAccountCredential(t.Context(), "lease-root", f.account.ID, f.account.Revision, "secret://dns/rotated")
						return err
					},
					func() error {
						_, err := f.repo.SetDNSAccountEnabled(t.Context(), "lease-root", f.account.ID, f.account.Revision, false)
						return err
					},
					func() error {
						_, err := f.repo.SetDomainDNSAccountBinding(t.Context(), "lease-root", f.item.ID, "", f.item.DNSBindingID, 0)
						return err
					},
					func() error { return f.repo.UpdateProjectDomain(t.Context(), "lease-root", f.item) },
					func() error { return NewSQLDomainRepository(db.DB, driver).Update(t.Context(), f.item) },
					func() error { return NewSQLDomainRepository(db.DB, driver).Delete(t.Context(), f.item.ID) },
				} {
					if err := mutate(); !errors.Is(err, ErrDNSAccountBusy) {
						t.Fatal("活动租约未阻止变更", err)
					}
				}
				f.exec(t, `UPDATE tasks SET status = 'failed' WHERE id = ?`, f.identity.TaskID)
				if err := NewSQLDomainRepository(db.DB, driver).UpdateCertificateState(t.Context(), f.item.ID, "failed", nil); err != nil {
					t.Fatal("活动租约不能阻止证书状态更新", err)
				}
				if err := NewSQLDomainRepository(db.DB, driver).Delete(t.Context(), f.item.ID); !errors.Is(err, ErrDNSAccountBusy) {
					t.Fatal("底层删除不能绕过终态任务的遗留租约", err)
				}
				if err := f.repo.DeleteProjectDomain(t.Context(), "lease-root", f.item.ID); !errors.Is(err, ErrDNSAccountBusy) {
					t.Fatal("终态任务仍有租约，不能删除域名", err)
				}
				if err := f.repo.ValidateDNSExecutionLease(t.Context(), lease); err == nil {
					t.Fatal("终态任务仍允许发起 DNS 操作")
				}
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				if err := f.repo.ReleaseDNSExecutionLease(cancelled, lease.ID); err == nil {
					t.Fatal("取消的释放应失败")
				}
				if _, err := f.repo.SetDNSAccountEnabled(t.Context(), "lease-root", f.account.ID, f.account.Revision, false); !errors.Is(err, ErrDNSAccountBusy) {
					t.Fatal("释放失败不能丢失引用", err)
				}
				if err := f.repo.ReleaseDNSExecutionLease(t.Context(), lease.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.repo.ReleaseDNSExecutionLease(t.Context(), lease.ID); err != nil {
					t.Fatal("释放应幂等", err)
				}
				if _, err := f.repo.SetDNSAccountEnabled(t.Context(), "lease-root", f.account.ID, f.account.Revision, false); err != nil {
					t.Fatal("正常释放后应允许禁用", err)
				}
				if err := NewSQLDomainRepository(db.DB, driver).Update(t.Context(), f.item); err != nil {
					t.Fatal("正常释放后应允许资料更新", err)
				}
				if err := NewSQLDomainRepository(db.DB, driver).Delete(t.Context(), f.item.ID); err != nil {
					t.Fatal("正常释放后应允许底层删除", err)
				}
			})
			for _, scenario := range []struct {
				name, column string
				value        any
			}{
				{"name", "name", "changed.example.test"},
				{"type", "certificate_type", "san"},
				{"sans", "sans_json", `["other.example.test"]`},
				{"renewal", "auto_renew", false},
				{"environment", "acme_environment", "production"},
				{"provider", "dns_provider", "aliyun"},
				{"challenge", "challenge_type", "http-01"},
			} {
				t.Run("same-timestamp-"+scenario.name, func(t *testing.T) {
					f := newDNSLeaseFixture(t, db.DB, driver)
					f.exec(t, `UPDATE domains SET `+scenario.column+` = ? WHERE id = ?`, scenario.value, f.item.ID)
					if _, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity); !errors.Is(err, ErrDNSExecutionInvalid) {
						t.Fatal("同时间戳的旧配置快照未拒绝", err)
					}
					var count int
					if err := db.DB.QueryRowContext(t.Context(), Rebind(driver, `SELECT COUNT(*) FROM dns_execution_leases WHERE domain_id = ?`), f.item.ID).Scan(&count); err != nil || count != 0 {
						t.Fatal("无效快照遗留执行租约", count, err)
					}
				})
			}
			t.Run("equivalent-sans-encoding", func(t *testing.T) {
				f := newDNSLeaseFixture(t, db.DB, driver)
				encoded, err := json.MarshalIndent(f.item.SANs, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				f.exec(t, `UPDATE domains SET sans_json = ? WHERE id = ?`, string(encoded), f.item.ID)
				lease, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity)
				if err != nil {
					t.Fatal("相同 SAN 不应因 JSON 格式而被拒绝", err)
				}
				if err := f.repo.ReleaseDNSExecutionLease(t.Context(), lease.ID); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("atomic-initial-binding", func(t *testing.T) {
				f := newDNSLeaseFixture(t, db.DB, driver)
				for _, scenario := range []string{"success", "old-revision", "wrong-provider", "duplicate-binding", "foreign-project", "http-challenge", "missing-account"} {
					t.Run(scenario, func(t *testing.T) {
						item, err := domain.NewDomain(domain.NewID()+".example.test", domain.CertificateSingle, nil)
						if err != nil {
							t.Fatal(err)
						}
						item.ProjectID, item.DNSProvider = f.item.ProjectID, "cloudflare"
						item.DNSAccountID, item.DNSBindingID, item.InitialDNSRevision = f.account.ID, domain.NewID(), f.account.Revision
						switch scenario {
						case "old-revision":
							item.InitialDNSRevision--
						case "wrong-provider":
							item.DNSProvider = "aliyun"
						case "duplicate-binding":
							item.DNSBindingID = f.item.DNSBindingID
						case "foreign-project":
							item.ProjectID = domain.DefaultProjectID
						case "http-challenge":
							item.ChallengeType = domain.ChallengeHTTP01
						case "missing-account":
							item.DNSAccountID = "missing"
						}
						err = f.repo.CreateProjectDomain(t.Context(), "lease-root", item)
						if (err == nil) != (scenario == "success") {
							t.Fatal("原子创建结果不符", err)
						}
						var count int
						if err := db.DB.QueryRowContext(t.Context(), Rebind(driver, `SELECT COUNT(*) FROM domains WHERE id = ?`), item.ID).Scan(&count); err != nil {
							t.Fatal(err)
						}
						if scenario != "success" && count != 0 {
							t.Fatal("绑定失败遗留未绑定域名")
						}
						if scenario == "success" {
							loaded, err := NewSQLDomainRepository(db.DB, driver).GetByID(t.Context(), item.ID)
							if err != nil || loaded.DNSAccountID != item.DNSAccountID || loaded.DNSBindingID != item.DNSBindingID {
								t.Fatal("创建后绑定不可见", err)
							}
						} else {
							if err := db.DB.QueryRowContext(t.Context(), Rebind(driver, `SELECT COUNT(*) FROM audit_logs WHERE resource_id = ?`), item.ID).Scan(&count); err != nil || count != 0 {
								t.Fatal("失败创建遗留成功审计", err)
							}
						}
					})
				}
			})
			for _, scenario := range []string{"missing-task", "wrong-attempt", "wrong-number", "cancelled-task", "finished-attempt", "wrong-project", "revoked", "anonymous", "fake-scheduler", "disabled", "unverified", "stale-binding", "stale-snapshot", "retry-without-permission"} {
				t.Run(scenario, func(t *testing.T) {
					f := newDNSLeaseFixture(t, db.DB, driver)
					switch scenario {
					case "missing-task":
						f.identity.TaskID = "missing"
					case "wrong-attempt":
						f.identity.AttemptID = "missing"
					case "wrong-number":
						f.identity.AttemptNumber = 2
					case "cancelled-task":
						f.exec(t, `UPDATE tasks SET status = 'cancelling' WHERE id = ?`, f.identity.TaskID)
					case "finished-attempt":
						f.exec(t, `UPDATE task_attempts SET status = 'failed' WHERE id = ?`, f.identity.AttemptID)
					case "wrong-project":
						f.item.ProjectID = domain.DefaultProjectID
					case "revoked":
						f.exec(t, `DELETE FROM project_members WHERE project_id = ?`, f.item.ProjectID)
					case "anonymous":
						f.exec(t, `UPDATE tasks SET operator_id = '' WHERE id = ?`, f.identity.TaskID)
					case "fake-scheduler":
						f.identity.Scheduled = true
					case "disabled":
						_, err = f.repo.SetDNSAccountEnabled(t.Context(), "lease-root", f.account.ID, f.account.Revision, false)
					case "unverified":
						_, err = f.repo.RecordDNSAccountVerification(t.Context(), "lease-root", f.account.ID, f.account.Revision, false)
					case "stale-binding":
						f.item.DNSBindingID = "old-binding"
					case "stale-snapshot":
						f.item.UpdatedAt = f.item.UpdatedAt.Add(-time.Hour)
					case "retry-without-permission":
						f.identity.AttemptNumber = 2
						f.exec(t, `UPDATE task_attempts SET attempt_number = 2 WHERE id = ?`, f.identity.AttemptID)
						f.exec(t, `DELETE FROM project_members WHERE project_id = ?`, f.item.ProjectID)
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity); err == nil {
						t.Fatal("无效身份或状态获得租约")
					}
				})
			}
			t.Run("scheduled-and-revalidation", func(t *testing.T) {
				f := newDNSLeaseFixture(t, db.DB, driver)
				f.exec(t, `UPDATE tasks SET operator_id = '', task_type = 'renewal_run' WHERE id = ?`, f.identity.TaskID)
				if _, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity); err == nil {
					t.Fatal("空操作者不能隐式成为调度器")
				}
				f.identity.Scheduled = true
				lease, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity)
				if err != nil {
					t.Fatal(err)
				}
				forged := lease
				forged.CredentialRef = "secret://dns/other"
				if err := f.repo.ValidateDNSExecutionLease(t.Context(), forged); err == nil {
					t.Fatal("篡改租约未拒绝")
				}
				f.exec(t, `UPDATE domains SET auto_renew = ? WHERE id = ?`, false, f.item.ID)
				if err := f.repo.ValidateDNSExecutionLease(t.Context(), lease); err == nil {
					t.Fatal("关闭续期后仍允许 DNS 写入")
				}
				if err := f.repo.ReleaseDNSExecutionLease(t.Context(), lease.ID); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("revocation-after-acquire", func(t *testing.T) {
				f := newDNSLeaseFixture(t, db.DB, driver)
				lease, err := f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity)
				if err != nil {
					t.Fatal(err)
				}
				f.exec(t, `DELETE FROM project_members WHERE project_id = ?`, f.item.ProjectID)
				if err := f.repo.ValidateDNSExecutionLease(t.Context(), lease); err == nil {
					t.Fatal("撤权后仍允许 DNS 写入")
				}
				if err := f.repo.ReleaseDNSExecutionLease(t.Context(), lease.ID); err != nil {
					t.Fatal("撤权不应阻止清理收尾", err)
				}
			})
			t.Run("rotation-race", func(t *testing.T) {
				for round := 0; round < 5; round++ {
					f := newDNSLeaseFixture(t, db.DB, driver)
					start := make(chan struct{})
					var wg sync.WaitGroup
					wg.Add(2)
					var lease domain.DNSExecutionLease
					var acquireErr, rotateErr error
					go func() {
						defer wg.Done()
						<-start
						lease, acquireErr = f.repo.AcquireDNSExecutionLease(t.Context(), f.item, f.identity)
					}()
					go func() {
						defer wg.Done()
						<-start
						_, rotateErr = f.repo.RotateDNSAccountCredential(t.Context(), "lease-root", f.account.ID, f.account.Revision, "secret://dns/"+domain.NewID())
					}()
					close(start)
					wg.Wait()
					if acquireErr == nil && rotateErr == nil {
						t.Fatal("获取租约与轮换不能同时成功")
					}
					if acquireErr == nil {
						if err := f.repo.ValidateDNSExecutionLease(t.Context(), lease); err != nil {
							t.Fatal("获胜租约失效", err)
						}
						if err := f.repo.ReleaseDNSExecutionLease(t.Context(), lease.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		})
	}
}
