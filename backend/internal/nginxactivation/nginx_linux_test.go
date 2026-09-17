package nginxactivation

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// 实机验收显式指定 Nginx 路径；仅启动独立进程，不使用系统站点、PID 或服务单元。
func TestRotationWithRealNginx(t *testing.T) {
	binary := os.Getenv("SCERT_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("未指定隔离验收使用的 Nginx 可执行文件")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("测试 Nginx 路径必须是绝对路径")
	}
	f := newRotationFixture(t)
	prefix := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(prefix, "ca.pem")
	writeTestFile(t, rootPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.authority.certificate.Raw}))
	configPath := filepath.Join(prefix, "nginx.conf")
	configuration := fmt.Sprintf(`worker_processes 1;
pid %s/nginx.pid;
error_log stderr warn;
events { worker_connections 64; }
http {
    access_log off;
    client_body_temp_path %s/client;
    proxy_temp_path %s/proxy;
    fastcgi_temp_path %s/fastcgi;
    uwsgi_temp_path %s/uwsgi;
    scgi_temp_path %s/scgi;
    server {
        listen %s ssl;
        server_name %s;
        include %s/active.conf;
        ssl_protocols TLSv1.2 TLSv1.3;
        location / {
            proxy_pass https://%s;
            proxy_set_header Host %s;
            proxy_ssl_server_name on;
            proxy_ssl_name %s;
            proxy_ssl_verify on;
            proxy_ssl_trusted_certificate %s;
        }
    }
}
`, prefix, prefix, prefix, prefix, prefix, prefix, address, testHostname,
		f.rotation.Directory, f.upstream.Address, net.JoinHostPort(testHostname, endpointPort(t, f.upstream.Address)), testHostname, rootPath)
	writeTestFile(t, configPath, []byte(configuration))
	reloader := CommandReloader{Binary: binary, Config: configPath, Prefix: prefix + "/"}
	if err := reloader.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.OpenFile(filepath.Join(prefix, "process.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-p", prefix+"/", "-c", configPath, "-g", "daemon off;")
	command.Stdout, command.Stderr = logFile, logFile
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGQUIT)
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("隔离 Nginx 退出失败：%v", err)
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-finished
			t.Error("隔离 Nginx 未在时限内退出")
		}
		_ = logFile.Close()
		if t.Failed() {
			content, _ := os.ReadFile(filepath.Join(prefix, "process.log"))
			t.Logf("隔离 Nginx 日志：\n%s", content)
		}
	})
	f.rotation.Proxy = Endpoint{Address: address, Hostname: testHostname, Roots: f.authority.roots}
	f.rotation.Reloader = reloader
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := f.rotation.Proxy.Wait(ctx, f.old.pair.Certificate[0]); err != nil {
		t.Fatal(err)
	}
	id, err := f.rotation.Stage(ctx, f.candidate.chain, f.candidate.key)
	if err != nil {
		t.Fatal(err)
	}
	f.upstream.active.Store(&f.candidate.pair)
	if err := f.rotation.Commit(ctx, id); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(t, "committed")
	next := f.authority.issue(t, testHostname, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
	if _, err := f.rotation.Stage(ctx, next.chain, next.key); err != nil {
		t.Fatal(err)
	}
	// 模拟调用方退出后由新组件恢复，真实入口必须重新提供上一张已提交证书。
	restarted := f.rotation
	if err := restarted.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	f.assertStatus(t, "rolled_back")
	if _, err := restarted.Proxy.Check(ctx, f.candidate.pair.Certificate[0]); err != nil {
		t.Fatal(err)
	}
}

func endpointPort(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestCommandReloaderRejectsRelativePolicy(t *testing.T) {
	for _, policy := range []CommandReloader{
		{Binary: "nginx", Config: "/etc/nginx/nginx.conf", Prefix: "/etc/nginx"},
		{Binary: "/usr/sbin/nginx", Config: "nginx.conf", Prefix: "/etc/nginx"},
		{Binary: "/usr/sbin/nginx", Config: "/etc/nginx/nginx.conf", Prefix: "."},
	} {
		if err := policy.Check(context.Background()); err == nil {
			t.Fatal("不应执行相对路径策略")
		}
	}
}
