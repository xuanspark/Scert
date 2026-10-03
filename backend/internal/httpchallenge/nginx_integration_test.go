//go:build linux

package httpchallenge

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 本测试只启动独立回环实例，不读取线上配置、不重载生产 Nginx。
func TestNginxHTTPChallengeRouting(t *testing.T) {
	binary := os.Getenv("SCERT_NGINX_TEST_BIN")
	if binary == "" {
		t.Skip("未启用真实 Nginx 隔离测试")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("Nginx 测试程序必须使用绝对路径")
	}
	root := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	store := NewStore()
	lease, err := store.NewLease(t.Context(), []string{"issued.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	const token = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	const value = token + "." + token
	if err := lease.Present(t.Context(), "issued.example.test", token, value); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		store.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	caPath := write("upstream-ca.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})))
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "nginx", "http-challenge.conf.example"))
	if err != nil {
		t.Fatal(err)
	}
	// 仅把系统信任库替换为此测试上游的 CA，证书验证与名称校验始终开启。
	render := func(name string) string {
		return strings.NewReplacer(
			"__UPSTREAM_PORT__", upstreamURL.Port(),
			"__MANAGEMENT_HOST__", name,
			"/etc/ssl/certs/ca-certificates.crt", caPath,
		).Replace(string(data))
	}
	good := write("challenge.conf", render(upstream.Certificate().DNSNames[0]))
	bad := write("untrusted-name.conf", render("wrong-upstream.invalid"))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`pid %s/nginx.pid;
error_log %s/error.log warn;
events { worker_connections 32; }
http {
    access_log %s/access.log;
    proxy_temp_path %s/proxy;
    client_body_temp_path %s/body;
    server {
        listen %s;
        server_name issued.example.test other.example.test;
        include %s;
        location / { return 308 https://$host$request_uri; }
    }
    server {
        listen %s;
        server_name bad-tls.example.test;
        include %s;
        location / { return 200 "existing-business"; }
    }
    server {
        listen %s;
        server_name business.example.test;
        location / { return 200 "existing-business"; }
    }
}
`, root, root, root, root, root, address, good, address, bad, address)
	configPath := write("nginx.conf", config)
	checkCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(checkCtx, binary, "-p", root, "-c", configPath, "-t").CombinedOutput(); err != nil {
		t.Fatalf("隔离 Nginx 配置错误：%v %s", err, output)
	}
	cmd := exec.Command(binary, "-p", root, "-c", configPath, "-g", "daemon off; master_process off;")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("隔离 Nginx 未及时退出")
		}
	})
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + address + "/")
		if err == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("隔离 Nginx 未就绪：", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, tc := range []struct {
		name, host, path, method string
		status                   int
		body                     string
	}{
		{"challenge", "issued.example.test", PathPrefix + token, "GET", 200, value},
		{"head", "issued.example.test", PathPrefix + token, "HEAD", 200, ""},
		{"wrong_host", "other.example.test", PathPrefix + token, "GET", 404, ""},
		{"unknown", "issued.example.test", PathPrefix + "unknown", "GET", 404, ""},
		{"query", "issued.example.test", PathPrefix + token + "?invalid=1", "GET", 404, ""},
		{"encoded", "issued.example.test", PathPrefix + "%61" + token[1:], "GET", 404, ""},
		{"post", "issued.example.test", PathPrefix + token, "POST", 404, ""},
		{"redirect", "issued.example.test", "/dashboard", "GET", 308, ""},
		{"business", "business.example.test", PathPrefix + token, "GET", 200, "existing-business"},
		{"tls_rejected", "bad-tls.example.test", PathPrefix + token, "GET", 502, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), tc.method, "http://"+address+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = tc.host
			request.Header.Set("X-Forwarded-Host", "issued.example.test")
			request.Header.Set("Cookie", "must-not-reach-upstream")
			request.Header.Set("Authorization", "must-not-reach-upstream")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status || tc.status != 308 && tc.status != 502 && string(body) != tc.body {
				t.Fatalf("路由结果不符：%d %s", response.StatusCode, body)
			}
			if tc.status == 308 && response.Header.Get("Location") != "https://issued.example.test/dashboard" {
				t.Fatal("普通业务重定向被挑战配置改变")
			}
			if tc.status == 404 || tc.name == "challenge" || tc.name == "head" {
				if response.Header.Get("Location") != "" || response.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("挑战响应被重定向或允许缓存")
				}
			}
		})
	}
}
