package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

func TestProxyPreflightStrictTLSAndResponseProof(t *testing.T) {
	for _, mode := range []string{"trusted", "untrusted-upstream", "untrusted-public", "wrong-response", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			material := completionDomainResource("admin.example.test")
			directory := t.TempDir()
			cert := filepath.Join(directory, "chain.pem")
			key := filepath.Join(directory, "key.pem")
			if err := os.WriteFile(cert, material.Certificate, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(key, material.PrivateKey, 0o600); err != nil {
				t.Fatal(err)
			}
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := reservation.Addr().(*net.TCPAddr).Port
			_ = reservation.Close()
			target, _ := url.Parse("https://127.0.0.1:" + strconv.Itoa(port))
			upstreamRoots := x509.NewCertPool()
			if mode != "untrusted-upstream" {
				upstreamRoots.AppendCertsFromPEM(material.Certificate)
			}
			reverse := httputil.NewSingleHostReverseProxy(target)
			reverse.ErrorLog = log.New(io.Discard, "", 0)
			upstream := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: upstreamRoots, ServerName: "admin.example.test"}}
			defer upstream.CloseIdleConnections()
			reverse.Transport = upstream
			proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "wrong-response" {
					_, _ = io.WriteString(w, "unrelated-service")
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
				reverse.ServeHTTP(w, r)
			}))
			defer proxy.Close()
			publicRoots := x509.NewCertPool()
			if mode != "untrusted-public" {
				publicRoots.AddCert(proxy.Certificate())
			}
			transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: publicRoots}, DisableKeepAlives: true}
			candidate := config.Config{
				Server: config.ServerConfig{Host: "127.0.0.1", Port: port, HTTPSMode: "proxy", ProxyHTTPSPort: proxy.Listener.Addr().(*net.TCPAddr).Port, TLSCert: cert, TLSKey: key},
				Web:    config.WebConfig{Hostname: "127.0.0.1"},
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err = verifySetupProxyTransport(ctx, candidate, transport)
			if (err == nil) != (mode == "trusted") {
				t.Fatalf("代理校验结果不正确：%s %v", mode, err)
			}
			listener, err := net.Listen("tcp", target.Host)
			if err != nil {
				t.Fatalf("预检未释放正式端口：%v", err)
			}
			_ = listener.Close()
		})
	}
}
