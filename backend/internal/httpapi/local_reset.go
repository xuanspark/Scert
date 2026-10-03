package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"git.pcux.cn/xuans/Scert/backend/internal/notification"
	"git.pcux.cn/xuans/Scert/backend/internal/passwordpolicy"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

type resetCodeSender func(context.Context, string, string) error

type localResetSession struct {
	server               *Server
	send                 resetCodeSender
	reserve              func() bool
	admin                config.AdminConfig
	expires              time.Time
	peer                 localreset.Peer
	mu                   sync.Mutex
	stage                int
	codeHash             [32]byte
	recoveryConfirmation deployment.RecoveryConfirmation
}

// LocalResetFactory 只交给受保护的本机通道，不注册到普通 HTTP 路由。
func (s *Server) LocalResetFactory() localreset.Factory {
	return s.localResetFactory(func(ctx context.Context, recipient, code string) error {
		s.configMu.RLock()
		cfg := s.config.Notification
		s.configMu.RUnlock()
		reader, ok := s.secrets.(notification.CredentialReader)
		if !ok {
			return localreset.ErrUnavailable
		}
		return notification.New(cfg, reader).SendSecurityCode(ctx, recipient, code)
	})
}

func (s *Server) localResetFactory(send resetCodeSender) localreset.Factory {
	var mu sync.Mutex
	var lastIssued time.Time
	reserve := func() bool {
		mu.Lock()
		defer mu.Unlock()
		now := s.now()
		if !lastIssued.IsZero() && now.Before(lastIssued.Add(time.Minute)) {
			return false
		}
		lastIssued = now
		return true
	}
	return func(ctx context.Context, peer localreset.Peer) (localreset.Session, error) {
		s.configMu.RLock()
		cfg := s.config
		s.configMu.RUnlock()
		if ctx.Err() != nil || !cfg.SetupReady || cfg.Admin.Email == "" ||
			cfg.Admin.PasswordHash == "" || strings.TrimSpace(s.configPath) == "" || send == nil {
			return nil, localreset.ErrUnavailable
		}
		return &localResetSession{server: s, send: send, reserve: reserve, admin: cfg.Admin,
			expires: s.now().Add(localreset.SessionTimeout), peer: peer}, nil
	}
}

func (r *localResetSession) Methods() []localreset.Method {
	return []localreset.Method{{ID: "email", Label: "绑定邮箱验证码"}}
}

func (r *localResetSession) current(ctx context.Context) bool {
	if ctx.Err() != nil || !r.server.now().Before(r.expires) {
		return false
	}
	r.server.configMu.RLock()
	defer r.server.configMu.RUnlock()
	return r.server.config.SetupReady && r.server.config.Admin == r.admin
}

func (r *localResetSession) Select(ctx context.Context, method string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stage != 0 || method != "email" || !r.current(ctx) {
		r.stage = -1
		return localreset.ErrProtocol
	}
	r.stage = -1
	if !r.reserve() {
		return localreset.ErrUnavailable
	}
	value, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return localreset.ErrUnavailable
	}
	code := fmt.Sprintf("%08d", value.Int64())
	r.codeHash = sha256.Sum256([]byte(code))
	if err := r.send(ctx, r.admin.Email, code); err != nil {
		r.audit("failed", "本机密码重置验证码发送失败")
		return localreset.ErrUnavailable
	}
	if !r.current(ctx) {
		return localreset.ErrProtocol
	}
	r.stage = 1
	return nil
}

func (r *localResetSession) Verify(ctx context.Context, code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	validStage := r.stage == 1
	r.stage = -1
	hash := sha256.Sum256([]byte(code))
	if !validStage || !r.current(ctx) || subtle.ConstantTimeCompare(hash[:], r.codeHash[:]) != 1 {
		r.codeHash = [32]byte{}
		r.audit("failed", "本机密码重置验证码无效或已过期")
		return localreset.ErrProtocol
	}
	r.codeHash = [32]byte{}
	r.stage = 2
	return nil
}

func (r *localResetSession) Reset(ctx context.Context, password string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	validStage := r.stage == 2
	r.stage = -1
	if !validStage || !r.current(ctx) {
		return localreset.ErrProtocol
	}
	if err := passwordpolicy.Validate(password, r.admin.Username); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return localreset.ErrUnavailable
	}
	s := r.server
	if !s.tlsMu.TryLock() {
		return localreset.ErrUnavailable
	}
	defer s.tlsMu.Unlock()
	if !r.current(ctx) {
		return localreset.ErrProtocol
	}
	persisted, err := config.Load(s.configPath)
	if err != nil || persisted.Admin != r.admin {
		return localreset.ErrUnavailable
	}
	previous := persisted
	persisted.Admin.PasswordHash = string(hash)
	if err := s.syncPrimarySuperAdmin(ctx, persisted); err != nil {
		r.audit("failed", "本机密码重置同步账户失败")
		return localreset.ErrUnavailable
	}
	if err := r.save(ctx, persisted); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		rollbackErr := s.syncPrimarySuperAdmin(rollbackCtx, previous)
		cancel()
		r.audit("failed", "本机密码重置保存配置失败")
		if rollbackErr != nil {
			return errors.Join(localreset.ErrUnavailable, errors.New("账户回滚失败，需要管理员检查配置与数据库"))
		}
		return localreset.ErrUnavailable
	}
	s.configMu.Lock()
	s.config.Admin = persisted.Admin
	s.configMu.Unlock()
	s.invalidateUserSessions("primary-super-admin")
	r.audit("success", "超级管理员已通过本机通道重置密码")
	return nil
}

func (r *localResetSession) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stage = -1
	r.codeHash = [32]byte{}
	r.recoveryConfirmation = deployment.RecoveryConfirmation{}
}

func (r *localResetSession) audit(status, message string) {
	if r.server.audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := r.server.audit.CreateAuditLog(ctx, repository.AuditLog{
		Action: "admin_local_password_reset", ResourceType: "super_admin", ResourceID: "primary-super-admin",
		Status: status, Message: message, CreatedAt: r.server.now().UTC(),
	})
	if err != nil && r.server.logger != nil {
		r.server.logger.Warn("本机密码重置审计写入失败")
	}
}

func (r *localResetSession) save(ctx context.Context, cfg config.Config) error {
	if !r.current(ctx) {
		return localreset.ErrProtocol
	}
	return cfg.Save(r.server.configPath)
}
