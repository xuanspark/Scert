package httpchallenge

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	notFound := func() { w.WriteHeader(http.StatusNotFound) }
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		notFound()
		return
	}
	if !strings.HasPrefix(r.URL.Path, PathPrefix) || r.URL.RawPath != "" || r.URL.RawQuery != "" {
		notFound()
		return
	}
	token := strings.TrimPrefix(r.URL.Path, PathPrefix)
	host := r.Host
	if strings.Contains(host, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(host)
		n, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || n < 1 || n > 65535 {
			notFound()
			return
		}
	}
	host, ok := normalizeHost(host)
	if s == nil || !ok || !validToken(token) {
		notFound()
		return
	}
	s.mu.Lock()
	s.prune(s.now())
	current, exists := s.entries[entryKey{host: host, token: token}]
	s.mu.Unlock()
	if !exists {
		notFound()
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(current.value)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(current.value))
	}
}
