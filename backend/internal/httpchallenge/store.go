package httpchallenge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

const PathPrefix = "/.well-known/acme-challenge/"

type entryKey struct {
	host  string
	token string
}

type entry struct {
	owner   *Lease
	value   string
	expires time.Time
}

// Store 仅在进程内保存短期挑战，不落盘、不跨订单共享挑战内容。
type Store struct {
	mu       sync.Mutex
	entries  map[entryKey]entry
	ttl      time.Duration
	capacity int
	now      func() time.Time
}

func NewStore() *Store {
	return &Store{entries: make(map[entryKey]entry), ttl: 15 * time.Minute, capacity: 4096, now: time.Now}
}

type Lease struct {
	store  *Store
	hosts  map[string]struct{}
	ctx    context.Context
	closed bool
	stop   func() bool
}

func (s *Store) NewLease(ctx context.Context, names []string) (*Lease, error) {
	if s == nil || len(names) == 0 || len(names) > 100 {
		return nil, errors.New("HTTP 验证服务未初始化或域名数量无效")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l := &Lease{store: s, ctx: ctx, hosts: make(map[string]struct{}, len(names))}
	for _, name := range names {
		host, ok := normalizeHost(name)
		if !ok {
			return nil, errors.New("HTTP 验证仅支持普通域名，不支持泛域名")
		}
		l.hosts[host] = struct{}{}
	}
	l.stop = context.AfterFunc(ctx, l.closeEntries)
	return l, nil
}

func normalizeHost(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	return name, len(name) <= 253 && !strings.HasPrefix(name, "*.") && domain.IsValidName(name) && strings.TrimSpace(name) == name
}

func validToken(token string) bool {
	if len(token) < 22 || len(token) > 256 {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (l *Lease) Present(ctx context.Context, name, token, keyAuth string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	host, ok := normalizeHost(name)
	if !ok || !validToken(token) || !strings.HasPrefix(keyAuth, token+".") || !validToken(strings.TrimPrefix(keyAuth, token+".")) {
		return errors.New("HTTP 挑战参数无效")
	}
	if _, ok := l.hosts[host]; !ok {
		return errors.New("HTTP 挑战域名不属于当前订单")
	}
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.closed || l.ctx.Err() != nil {
		return errors.New("HTTP 验证订单已关闭")
	}
	now := s.now()
	s.prune(now)
	key := entryKey{host: host, token: token}
	if existing, exists := s.entries[key]; exists {
		if existing.owner != l || existing.value != keyAuth {
			return errors.New("HTTP 挑战令牌已被其他验证占用")
		}
		return nil
	}
	if len(s.entries) >= s.capacity {
		return errors.New("HTTP 挑战容量已满，请稍后重试")
	}
	s.entries[key] = entry{owner: l, value: keyAuth, expires: now.Add(s.ttl)}
	return nil
}

// CleanUp 不受已取消上下文影响，且只清理当前订单拥有的挑战。
func (l *Lease) CleanUp(_ context.Context, name, token, keyAuth string) error {
	host, _ := normalizeHost(name)
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	key := entryKey{host: host, token: token}
	if current, exists := s.entries[key]; exists && current.owner == l && current.value == keyAuth {
		delete(s.entries, key)
	}
	return nil
}

func (l *Lease) Close() {
	if l.stop != nil {
		l.stop()
	}
	l.closeEntries()
}

func (l *Lease) closeEntries() {
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	l.closed = true
	for key, current := range s.entries {
		if current.owner == l {
			delete(s.entries, key)
		}
	}
}

// prune 的调用者必须持有锁；过期或取消的挑战不能继续对外提供。
func (s *Store) prune(now time.Time) {
	for key, current := range s.entries {
		if !now.Before(current.expires) || current.owner.ctx.Err() != nil {
			delete(s.entries, key)
		}
	}
}
