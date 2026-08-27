package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

const (
	ResetPath       = "/reset"
	stateFileName   = "scert-admin-reset.json"
	defaultLease    = 90 * time.Second
	RefreshInterval = 30 * time.Second
)

var ErrResetSessionClosed = errors.New("密码重置会话已关闭")

type State struct {
	Active    bool      `json:"active"`
	ResetURL  string    `json:"reset_url"`
	CodeHash  string    `json:"code_hash"`
	ProcessID int       `json:"process_id"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Session struct {
	State State
	Code  string
}

type Controller struct {
	config        config.Config
	statePath     string
	leaseDuration time.Duration
	now           func() time.Time
	state         State
}

func New(cfg config.Config) *Controller {
	return &Controller{
		config:        cfg,
		statePath:     StatePath(cfg.Paths.Data),
		leaseDuration: defaultLease,
		now:           time.Now,
	}
}

func StatePath(dataDir string) string {
	return filepath.Join(dataDir, stateFileName)
}

func (c *Controller) Start() (Session, error) {
	if c == nil {
		return Session{}, fmt.Errorf("Scert-admin 控制器不能为空")
	}
	if !c.config.SetupReady {
		return Session{}, fmt.Errorf("Scert 尚未完成初始化，不能启用密码重置入口")
	}
	resetURL := c.config.ResetURL()
	if resetURL == "" {
		return Session{}, fmt.Errorf("未配置管理页面域名或公网 IP，无法生成密码重置地址")
	}
	if active, err := IsActive(c.statePath, c.now()); err != nil {
		return Session{}, err
	} else if active {
		return Session{}, fmt.Errorf("密码重置入口已经启用")
	}
	code, err := generateCode()
	if err != nil {
		return Session{}, err
	}
	now := c.now().UTC()
	c.state = State{
		Active: true, ResetURL: resetURL, CodeHash: hashCode(code), ProcessID: os.Getpid(),
		StartedAt: now, ExpiresAt: now.Add(c.leaseDuration),
	}
	if err := writeState(c.statePath, c.state); err != nil {
		return Session{}, err
	}
	return Session{State: c.state, Code: code}, nil
}

func (c *Controller) Refresh() error {
	if c == nil || !c.state.Active {
		return fmt.Errorf("密码重置入口尚未启用")
	}
	latest, err := ReadState(c.statePath)
	if err != nil {
		return err
	}
	if !latest.Active || latest.CodeHash != c.state.CodeHash || !c.now().UTC().Before(latest.ExpiresAt) {
		c.state.Active = false
		return ErrResetSessionClosed
	}
	c.state = latest
	c.state.ExpiresAt = c.now().UTC().Add(c.leaseDuration)
	return writeState(c.statePath, c.state)
}

func (c *Controller) Stop() error {
	if c == nil || !c.state.Active {
		return nil
	}
	c.state.Active = false
	c.state.ExpiresAt = c.now().UTC()
	return writeState(c.statePath, c.state)
}

func IsActive(path string, now time.Time) (bool, error) {
	state, err := ReadState(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.Active && now.UTC().Before(state.ExpiresAt), nil
}

func VerifyCode(path, code string, now time.Time) (State, error) {
	state, err := ReadState(path)
	if err != nil {
		return State{}, err
	}
	if !state.Active || state.CodeHash == "" || !now.UTC().Before(state.ExpiresAt) {
		return State{}, ErrResetSessionClosed
	}
	expected := hashCode(code)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(state.CodeHash)) != 1 {
		return State{}, ErrResetSessionClosed
	}
	return state, nil
}

func Consume(path string, session State, now time.Time) error {
	latest, err := ReadState(path)
	if err != nil {
		return err
	}
	if !latest.Active || latest.CodeHash == "" || !now.UTC().Before(latest.ExpiresAt) {
		return ErrResetSessionClosed
	}
	if subtle.ConstantTimeCompare([]byte(latest.CodeHash), []byte(session.CodeHash)) != 1 {
		return ErrResetSessionClosed
	}
	latest.Active = false
	latest.CodeHash = ""
	latest.ExpiresAt = now.UTC()
	return writeState(path, latest)
}

func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("解析密码重置激活状态失败：%w", err)
	}
	return state, nil
}

func writeState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建密码重置状态目录失败：%w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码密码重置激活状态失败：%w", err)
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("保存密码重置临时状态失败：%w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("设置密码重置临时状态权限失败：%w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("保存密码重置激活状态失败：%w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置密码重置状态文件权限失败：%w", err)
	}
	return nil
}

func generateCode() (string, error) {
	buffer := make([]byte, 10)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("生成一次性重置校验码失败：%w", err)
	}
	value := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buffer)
	return strings.ToUpper(value[:5] + "-" + value[5:10] + "-" + value[10:]), nil
}

func hashCode(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToUpper(value))))
	return hex.EncodeToString(sum[:])
}
