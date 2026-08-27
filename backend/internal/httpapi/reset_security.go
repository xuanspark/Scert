package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	resetCSRFTokenName = "scert_reset_csrf"
	resetRateWindow    = 15 * time.Minute
	resetRateLimit     = 5
	resetMaxBodyBytes  = 16 << 10
)

type resetRateLimiter struct {
	mu      sync.Mutex
	failure map[string][]time.Time
}

func newResetRateLimiter() *resetRateLimiter {
	return &resetRateLimiter{failure: make(map[string][]time.Time)}
}

func (l *resetRateLimiter) allowed(ip string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(ip, now)
	return len(l.failure[ip]) < resetRateLimit
}

func (l *resetRateLimiter) failed(ip string, now time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(ip, now)
	l.failure[ip] = append(l.failure[ip], now)
}

func (l *resetRateLimiter) clear(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.failure, ip)
	l.mu.Unlock()
}

func (l *resetRateLimiter) pruneLocked(ip string, now time.Time) {
	cutoff := now.Add(-resetRateWindow)
	items := l.failure[ip]
	keep := items[:0]
	for _, item := range items {
		if item.After(cutoff) {
			keep = append(keep, item)
		}
	}
	if len(keep) == 0 {
		delete(l.failure, ip)
		return
	}
	l.failure[ip] = keep
}

func resetClientIP(request *http.Request) string {
	if request == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(request.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if value := strings.TrimSpace(request.RemoteAddr); value != "" {
		return value
	}
	return "unknown"
}
