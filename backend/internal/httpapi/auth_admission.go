package httpapi

import (
	"net/http"
	"strings"
	"time"
)

const (
	loginConcurrentLimit       = 8
	loginSourceConcurrentLimit = 2
	loginSourceRequestLimit    = 30
)

type loginSource struct {
	WindowStart time.Time
	Requests    int
	InFlight    int
}

// 在密码计算和数据库查询之前预约容量，不建立无限等待队列。
// 来源只取真实 TCP 对端；不信任任意转发头。代理部署应另外配置边缘限流。
func (s *Server) reserveLogin(request *http.Request, key string) bool {
	sourceKey := strings.SplitN(loginAttemptKey(request, ""), "|", 2)[0]
	now := s.now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.pruneLoginAttemptsLocked(now)
	for name, source := range s.loginSources {
		if source.InFlight == 0 && now.Sub(source.WindowStart) >= time.Minute {
			delete(s.loginSources, name)
		}
	}
	source, exists := s.loginSources[sourceKey]
	attempt, knownAttempt := s.loginAttempts[key]
	if attempt.BlockedTill.After(now) || attempt.Failures+s.loginPending[key] >= loginFailureLimit ||
		s.loginInFlight >= loginConcurrentLimit || source.InFlight >= loginSourceConcurrentLimit ||
		(!knownAttempt && len(s.loginAttempts) >= authStateCapacity) ||
		(!exists && len(s.loginSources) >= authStateCapacity) {
		return false
	}
	if source.WindowStart.IsZero() || now.Sub(source.WindowStart) >= time.Minute {
		source.WindowStart = now
		source.Requests = 0
	}
	if source.Requests >= loginSourceRequestLimit {
		return false
	}
	source.Requests++
	source.InFlight++
	s.loginSources[sourceKey] = source
	s.loginInFlight++
	s.loginPending[key]++
	return true
}

func (s *Server) releaseLogin(request *http.Request, key string) {
	sourceKey := strings.SplitN(loginAttemptKey(request, ""), "|", 2)[0]
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	source := s.loginSources[sourceKey]
	source.InFlight--
	s.loginSources[sourceKey] = source
	s.loginInFlight--
	s.loginPending[key]--
	if s.loginPending[key] == 0 {
		delete(s.loginPending, key)
	}
}
