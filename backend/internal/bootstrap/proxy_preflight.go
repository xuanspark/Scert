package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// verifySetupProxy 使用候选证书临时监听真实上游端口，再从公开域名穿过代理核验随机响应。
// 不跟随重定向、不使用环境代理、不关闭 TLS 校验；失败时不提交初始化配置。
func verifySetupProxy(ctx context.Context, candidate config.Config) error {
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext:         (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSHandshakeTimeout: 3 * time.Second, DisableKeepAlives: true,
	}
	return verifySetupProxyTransport(ctx, candidate, transport)
}

func verifySetupProxyTransport(ctx context.Context, candidate config.Config, transport *http.Transport) error {
	defer transport.CloseIdleConnections()
	if candidate.Server.HTTPSMode != "proxy" {
		return nil
	}
	material, err := tls.LoadX509KeyPair(candidate.Server.TLSCert, candidate.Server.TLSKey)
	if err != nil {
		return err
	}
	host := strings.Trim(strings.TrimSpace(candidate.Server.Host), "[]")
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			host = "127.0.0.1"
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(candidate.Server.Port)))
	if err != nil {
		return err
	}
	defer listener.Close()
	token := rand.Text()
	path := "/.well-known/scert-setup/" + rand.Text()
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet || r.URL.Path != path {
				http.Error(w, "管理入口正在验证", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, token)
		}),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{material}},
		ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxHeaderBytes: 8192,
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.ServeTLS(listener, "", "") }()
	defer func() { _ = server.Close(); <-done }()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	address := strings.TrimRight(candidate.ManagementURL(), "/") + path
	for successes := 0; ; {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		valid := false
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 256))
			_ = response.Body.Close()
			valid = readErr == nil && response.StatusCode == http.StatusOK && string(body) == token
		}
		if valid {
			successes++
		} else {
			successes = 0
		}
		if successes >= 3 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("公开代理入口未能连续通过候选上游证书及随机响应校验")
		case <-time.After(150 * time.Millisecond):
		}
	}
}
