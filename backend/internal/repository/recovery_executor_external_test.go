package repository

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/postgrestest"
)

type recoveryExternalFixture struct {
	database func(*testing.T) config.DatabaseConfig
	inject   func(*testing.T, *sql.DB, config.DatabaseConfig, string)
}

func TestRecoveryExecutorPostgresReal(t *testing.T) {
	server := postgrestest.New(t)
	runExternalRecovery(t, recoveryExternalFixture{database: server.Database, inject: func(t *testing.T, db *sql.DB, _ config.DatabaseConfig, stage string) {
		for _, query := range []string{
			`CREATE FUNCTION reject_recovery_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type = '` + stage + `' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$`,
			`CREATE TRIGGER reject_recovery_event BEFORE INSERT ON recovery_events FOR EACH ROW EXECUTE FUNCTION reject_recovery_event()`,
		} {
			if _, err := db.ExecContext(t.Context(), query); err != nil {
				t.Fatal("安装隔离审计故障失败", err)
			}
		}
	}})
}

func TestRecoveryExecutorMySQLReal(t *testing.T) {
	server := mysqltest.New(t)
	runExternalRecovery(t, recoveryExternalFixture{database: server.Database, inject: func(t *testing.T, _ *sql.DB, cfg config.DatabaseConfig, stage string) {
		parsed, err := mysql.ParseDSN(cfg.DSN)
		if err != nil || !strings.HasPrefix(parsed.DBName, "scert_test_") {
			t.Fatal("拒绝对非隔离库注入故障")
		}
		schema := "`" + strings.ReplaceAll(parsed.DBName, "`", "``") + "`"
		query := `CREATE TRIGGER ` + schema + `.reject_recovery_event BEFORE INSERT ON ` + schema + `.recovery_events FOR EACH ROW BEGIN IF NEW.event_type = '` + stage + `' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected'; END IF; END`
		// MySQL 开启二进制日志时，仅隔离管理连接创建触发器；业务账户不提权。
		if _, err := server.Admin().ExecContext(t.Context(), query); err != nil {
			t.Fatal("安装隔离审计故障失败", err)
		}
	}})
}

func openRecoveryExternal(t *testing.T, cfg config.DatabaseConfig) *sql.DB {
	t.Helper()
	db, err := database.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal("连接隔离实库失败")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.DB
}

// 只替换远端文件实现，准备、确认、执行、迁移、占用和审计均走实际产品代码。
func runExternalRecovery(t *testing.T, fixture recoveryExternalFixture) {
	t.Helper()
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		t.Run("reconciliation/"+string(action), func(t *testing.T) {
			cfg := fixture.database(t)
			db := openRecoveryExternal(t, cfg)
			if err := database.Migrate(t.Context(), db, cfg.Driver); err != nil {
				t.Fatal(err)
			}
			runReconciliationSuccess(t, NewSQLDeploymentTransactionRepository(db, cfg.Driver), db, action)
		})
	}
	for _, action := range []deployment.RecoveryAction{deployment.RecoveryActionRecord, deployment.RecoveryActionCommit, deployment.RecoveryActionRollback} {
		for _, stage := range []string{"none", "started", "finished"} {
			t.Run(string(action)+"/"+stage, func(t *testing.T) {
				cfg := fixture.database(t)
				db := openRecoveryExternal(t, cfg)
				if err := database.Migrate(t.Context(), db, cfg.Driver); err != nil {
					t.Fatal(err)
				}
				repo, _, service, client, server, record := seedRecoveryExecutor(t, NewSQLDeploymentTransactionRepository(db, cfg.Driver), db)
				before, err := repo.ListPendingDeploymentTransactions(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				filesBefore := maps.Clone(client.files)
				resolve := func(context.Context, string) (deployment.Server, error) { return server, nil }
				confirmation, token, err := service.PrepareRecoveryConfirmation(t.Context(), record.ID, "unix:0", action, resolve)
				if err != nil {
					t.Fatal("实库准备失败", err)
				}
				if stage != "none" {
					fixture.inject(t, db, cfg, stage)
				}
				item, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve)
				if stage == "none" {
					if err != nil || item.Status != "succeeded" {
						t.Fatal("实库执行未成功", err)
					}
					recoveryCount(t, db, "recovery_dispositions", 1)
					recoveryCount(t, db, "recovery_claims", 0)
					recoveryCount(t, db, "recovery_events", 2)
				} else if !errors.Is(err, deployment.ErrRecoveryDispositionUnavailable) || item.Status == "succeeded" {
					t.Fatal("审计故障未拒绝成功结果", err)
				}
				if _, err := service.ExecuteRecoveryDisposition(t.Context(), confirmation, "unix:0", token, resolve); !errors.Is(err, deployment.ErrRecoveryConfirmation) {
					t.Fatal("实库执行确认被重放", err)
				}
				// 实际关闭连接池后重新打开，不能靠旧仓储对象的内存证明持久性。
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				db = openRecoveryExternal(t, cfg)
				if err := database.Migrate(t.Context(), db, cfg.Driver); err != nil {
					t.Fatal("实库重开迁移失败", err)
				}
				repo = NewSQLDeploymentTransactionRepository(db, cfg.Driver)
				pending, err := repo.ListPendingDeploymentTransactions(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if stage == "started" {
					for _, table := range []string{"recovery_dispositions", "recovery_claims", "recovery_events"} {
						recoveryCount(t, db, table, 0)
					}
					if !reflect.DeepEqual(before, pending) || !reflect.DeepEqual(filesBefore, client.files) {
						t.Fatal("开始审计失败修改了原事务或远端")
					}
					return
				}
				stored, err := repo.GetRecoveryDisposition(t.Context(), confirmation.DispositionID())
				if err != nil {
					t.Fatal("重开后处置记录丢失", err)
				}
				restarted := deployment.NewServiceWithJournal(nil, repo)
				view, err := restarted.GetRecoveryDisposition(t.Context(), confirmation.DispositionID())
				if err != nil || view.Validate() != nil || !reflect.DeepEqual(view, stored.Summary()) {
					t.Fatal("重开后脱敏查询与持久结果不一致", err)
				}
				if stage == "finished" {
					recoveryCount(t, db, "recovery_claims", 1)
					recoveryCount(t, db, "recovery_events", 1)
					if stored.Status != "running" || len(pending) != 1 || pending[0].Status != deployment.TransactionStatusRecoveryRequired {
						t.Fatal("结束审计失败丢失占用或发布了事务终态")
					}
					if err := restarted.RecoverPending(t.Context(), func(context.Context, string) (deployment.Server, error) {
						t.Error("重开后自动恢复重复执行人工占用事务")
						return deployment.Server{}, errors.New("禁止解析远端")
					}); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
						t.Fatal("重开后未遵守持久占用", err)
					}
				} else if stored.Status != "succeeded" {
					t.Fatal("成功结果重开后丢失")
				}
				if action == deployment.RecoveryActionRecord {
					if !reflect.DeepEqual(before, pending) || !reflect.DeepEqual(filesBefore, client.files) {
						t.Fatal("仅记录动作改变原事务、证据或远端文件")
					}
				} else {
					expected, result := "new", deployment.RecoveryResultCommitted
					if action == deployment.RecoveryActionRollback {
						expected, result = "old", deployment.RecoveryResultRolledBack
					}
					for _, path := range record.Paths {
						if client.files[path] != expected {
							t.Fatal("远端动作与明确选择不一致")
						}
					}
					if stage == "none" && (len(pending) != 0 || stored.ResultCode != result) {
						t.Fatal("成功结果与原事务终态不一致")
					}
				}
			})
		}
	}
	t.Run("concurrent_idempotency", func(t *testing.T) {
		cfg := fixture.database(t)
		db := openRecoveryExternal(t, cfg)
		if err := database.Migrate(t.Context(), db, cfg.Driver); err != nil {
			t.Fatal(err)
		}
		repo, _, intent := seedRecoveryDisposition(t, NewSQLDeploymentTransactionRepository(db, cfg.Driver), db, deployment.RecoveryActionRecord)
		var created atomic.Int32
		var group sync.WaitGroup
		for range 8 {
			group.Go(func() {
				_, fresh, err := repo.BeginRecoveryDisposition(t.Context(), intent)
				if err != nil {
					t.Errorf("并发幂等请求失败：%v", err)
				}
				if fresh {
					created.Add(1)
				}
			})
		}
		group.Wait()
		if created.Load() != 1 {
			t.Fatal("并发请求没有精确创建一次处置")
		}
		conflict := intent
		conflict.Operator = "unix:1"
		if _, _, err := repo.BeginRecoveryDisposition(t.Context(), conflict); !errors.Is(err, deployment.ErrRecoveryDispositionConflict) {
			t.Fatal("不同操作者复用了幂等结果", err)
		}
		conflict = intent
		conflict.ID, conflict.RequestKey = strings.Repeat("e", 64), strings.Repeat("f", 64)
		if _, _, err := repo.BeginRecoveryDisposition(t.Context(), conflict); !errors.Is(err, deployment.ErrRecoveryDispositionBusy) {
			t.Fatal("其他请求覆盖已有占用", err)
		}
		for _, table := range []string{"recovery_dispositions", "recovery_claims", "recovery_events"} {
			recoveryCount(t, db, table, 1)
		}
	})
}
