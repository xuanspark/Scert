package httpapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

func newLocalResetTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := readyTestConfig(t.TempDir())
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "bound@example.test"
	cfg.Admin.PasswordHash = httpUserPasswordHash(t, "OldPassword123")
	cfg.Web.Hostname = "scert.example.test"
	cfg.DNS.VerificationState = "verified"
	path := filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatal(err)
	}
	return New(Dependencies{Config: cfg, ConfigPath: path, Repository: &stubDomainRepository{}, Users: users})
}

func preparedLocalReset(t *testing.T, server *Server) *localResetSession {
	t.Helper()
	var code string
	factory := server.localResetFactory(func(_ context.Context, recipient, value string) error {
		if recipient != "bound@example.test" {
			t.Fatal("验证码收件人不是绑定邮箱")
		}
		code = value
		return nil
	})
	session, err := factory(t.Context(), localreset.Peer{Identity: "test-local-admin"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	if err := session.Select(t.Context(), "email"); err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 {
		t.Fatal("验证码长度不正确")
	}
	if err := session.Verify(t.Context(), code); err != nil {
		t.Fatal(err)
	}
	return session.(*localResetSession)
}

func assertLocalResetUnchanged(t *testing.T, server *Server, hash string) {
	t.Helper()
	if server.config.Admin.PasswordHash != hash {
		t.Fatal("失败流程修改了内存密码")
	}
	user, err := server.users.GetUserByID(t.Context(), "primary-super-admin")
	if err != nil {
		t.Fatal(err)
	}
	if user.PasswordHash != hash {
		t.Fatal("失败流程修改了数据库密码")
	}
}

func TestLocalResetPersistsAndRevokesOnlyAdminSessions(t *testing.T) {
	server := newLocalResetTestServer(t)
	oldHash := server.config.Admin.PasswordHash
	server.authSessions["old-admin"] = authSession{UserID: "primary-super-admin"}
	server.authSessions["other-user"] = authSession{UserID: "ordinary-user"}
	session := preparedLocalReset(t, server)
	if err := session.Reset(t.Context(), "NewPass123!"); err != nil {
		t.Fatal(err)
	}
	persisted, err := config.Load(server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := server.users.GetUserByID(t.Context(), "primary-super-admin")
	if err != nil {
		t.Fatal(err)
	}
	hash := server.config.Admin.PasswordHash
	if hash == oldHash || hash != persisted.Admin.PasswordHash || hash != user.PasswordHash {
		t.Fatal("密码没有一致写入配置、数据库和内存")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("NewPass123!")) != nil {
		t.Fatal("新密码校验失败")
	}
	if _, ok := server.authSessions["old-admin"]; ok {
		t.Fatal("旧管理员会话仍然有效")
	}
	if _, ok := server.authSessions["other-user"]; !ok {
		t.Fatal("误注销其他用户")
	}
	if err := session.Reset(t.Context(), "NextPass123!"); err == nil {
		t.Fatal("允许重放密码重置")
	}
	if session.codeHash != [32]byte{} {
		t.Fatal("验证码摘要未清除")
	}
}

func TestLocalResetRejectsInvalidState(t *testing.T) {
	for _, scenario := range []string{"wrong-code", "expired", "changed-admin", "cancelled", "closed", "skip-select"} {
		t.Run(scenario, func(t *testing.T) {
			server := newLocalResetTestServer(t)
			oldHash := server.config.Admin.PasswordHash
			now := time.Now()
			server.now = func() time.Time { return now }
			var code string
			factory := server.localResetFactory(func(_ context.Context, _, value string) error { code = value; return nil })
			session, err := factory(t.Context(), localreset.Peer{Identity: "test-local-admin"})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if scenario != "skip-select" {
				if err := session.Select(t.Context(), "email"); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "wrong-code":
				code = "invalid-code"
			case "expired":
				now = now.Add(localreset.SessionTimeout)
			case "changed-admin":
				server.config.Admin.Email = "changed@example.test"
			case "cancelled":
				cancel()
			case "closed":
				session.Close()
			}
			if err := session.Verify(ctx, code); err == nil {
				t.Fatal("无效会话通过验证")
			}
			if err := session.Reset(t.Context(), "NewPass123!"); err == nil {
				t.Fatal("无效会话允许重置密码")
			}
			assertLocalResetUnchanged(t, server, oldHash)
		})
	}
}

func TestLocalResetRejectsUnavailableSetupAndChannel(t *testing.T) {
	for _, scenario := range []string{"bootstrap", "missing-email", "missing-hash", "missing-path", "nil-sender", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			server := newLocalResetTestServer(t)
			sender := resetCodeSender(func(context.Context, string, string) error { return nil })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "bootstrap":
				server.config.SetupReady = false
			case "missing-email":
				server.config.Admin.Email = ""
			case "missing-hash":
				server.config.Admin.PasswordHash = ""
			case "missing-path":
				server.configPath = ""
			case "nil-sender":
				sender = nil
			case "cancelled":
				cancel()
			}
			if session, err := server.localResetFactory(sender)(ctx, localreset.Peer{}); err == nil || session != nil {
				t.Fatal("未就绪的服务创建了重置会话")
			}
		})
	}
}

func TestLocalResetMailCooldownIsSharedAndConcurrent(t *testing.T) {
	server := newLocalResetTestServer(t)
	now := time.Now()
	server.now = func() time.Time { return now }
	var sends atomic.Int32
	factory := server.localResetFactory(func(context.Context, string, string) error { sends.Add(1); return nil })
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			session, err := factory(t.Context(), localreset.Peer{Identity: "test-local-admin"})
			if err != nil {
				t.Error(err)
				return
			}
			defer session.Close()
			_ = session.Select(t.Context(), "email")
		}()
	}
	wait.Wait()
	if sends.Load() != 1 {
		t.Fatal("并发会话绕过了发送冷却")
	}
	now = now.Add(time.Minute)
	session, err := factory(t.Context(), localreset.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Select(t.Context(), "email"); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 2 {
		t.Fatal("冷却到期后仍无法发送")
	}
}

type localResetFaultUsers struct {
	repository.UserRepository
	ensure func(context.Context, repository.User) error
}

func (r *localResetFaultUsers) EnsureSuperAdmin(ctx context.Context, user repository.User) error {
	return r.ensure(ctx, user)
}

func TestLocalResetPersistenceFailureBoundaries(t *testing.T) {
	for _, scenario := range []string{"database", "config-save", "cancel-after-database", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			server := newLocalResetTestServer(t)
			oldHash := server.config.Admin.PasswordHash
			session := preparedLocalReset(t, server)
			base := server.users
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server.users = &localResetFaultUsers{UserRepository: base, ensure: func(callCtx context.Context, user repository.User) error {
				calls++
				if calls == 1 && scenario == "database" {
					return errors.New("injected-database-failure")
				}
				if calls == 2 {
					if callCtx.Err() != nil {
						t.Fatal("补偿回滚继承了取消状态")
					}
					deadline, ok := callCtx.Deadline()
					if !ok || time.Until(deadline) > 6*time.Second {
						t.Fatal("补偿回滚没有超时上限")
					}
					if scenario == "rollback" {
						return errors.New("injected-rollback-failure")
					}
				}
				if err := base.EnsureSuperAdmin(callCtx, user); err != nil {
					return err
				}
				if calls == 1 {
					if scenario == "cancel-after-database" {
						cancel()
					}
					if scenario == "config-save" || scenario == "rollback" {
						if err := os.Rename(server.configPath, server.configPath+".preserved"); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(server.configPath, 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				return nil
			}}
			if err := session.Reset(ctx, "NewPass123!"); !errors.Is(err, localreset.ErrUnavailable) {
				t.Fatalf("失败流程报告了成功或错误类型不正确：%v", err)
			}
			if scenario == "database" {
				if calls != 1 {
					t.Fatal("数据库失败后不应继续提交")
				}
			} else if calls != 2 {
				t.Fatal("持久化失败没有尝试回滚数据库")
			}
			if server.config.Admin.PasswordHash != oldHash {
				t.Fatal("失败时更新了内存密码")
			}
			if scenario != "rollback" {
				assertLocalResetUnchanged(t, server, oldHash)
			}
			if scenario == "database" || scenario == "cancel-after-database" {
				persisted, err := config.Load(server.configPath)
				if err != nil || persisted.Admin.PasswordHash != oldHash {
					t.Fatal("失败时更改了配置密码")
				}
			}
		})
	}
}

func TestLocalResetConcurrentVerifiedSessionsOnlyOneCommits(t *testing.T) {
	server := newLocalResetTestServer(t)
	first := preparedLocalReset(t, server)
	second := preparedLocalReset(t, server)
	var successes atomic.Int32
	var wait sync.WaitGroup
	for _, session := range []*localResetSession{first, second} {
		wait.Add(1)
		go func(session *localResetSession) {
			defer wait.Done()
			if err := session.Reset(t.Context(), "NewPass123!"); err == nil {
				successes.Add(1)
			}
		}(session)
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatal("并发重置没有保证单次提交")
	}
}

func TestLocalResetFailureConsumesVerification(t *testing.T) {
	for _, scenario := range []string{"weak-password", "config-conflict", "locked"} {
		t.Run(scenario, func(t *testing.T) {
			server := newLocalResetTestServer(t)
			oldHash := server.config.Admin.PasswordHash
			session := preparedLocalReset(t, server)
			password := "NewPass123!"
			switch scenario {
			case "weak-password":
				password = "short"
			case "config-conflict":
				cfg, err := config.Load(server.configPath)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Admin.Email = "changed@example.test"
				if err := cfg.Save(server.configPath); err != nil {
					t.Fatal(err)
				}
			case "locked":
				server.tlsMu.Lock()
				defer server.tlsMu.Unlock()
			}
			if err := session.Reset(t.Context(), password); err == nil {
				t.Fatal("失败条件被忽略")
			}
			if err := session.Reset(t.Context(), "NextPass123!"); err == nil {
				t.Fatal("失败后允许重用验证码")
			}
			assertLocalResetUnchanged(t, server, oldHash)
		})
	}
}

func TestLocalResetSendFailureConsumesSession(t *testing.T) {
	server := newLocalResetTestServer(t)
	var code string
	session, err := server.localResetFactory(func(_ context.Context, _, value string) error {
		code = value
		return errors.New("injected-send-failure")
	})(t.Context(), localreset.Peer{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Select(t.Context(), "email"); err == nil {
		t.Fatal("发送失败被忽略")
	}
	if err := session.Verify(t.Context(), code); err == nil {
		t.Fatal("发送失败仍可验证")
	}
	if err := session.Select(t.Context(), "email"); err == nil {
		t.Fatal("失败会话被重新使用")
	}
	if session.(*localResetSession).codeHash == sha256.Sum256([]byte(code)) {
		t.Fatal("失败验证没有清理摘要")
	}
}
