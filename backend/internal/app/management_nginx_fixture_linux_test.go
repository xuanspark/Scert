package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/nginxactivation"
)

func startApplicationNginx(t *testing.T, f *applicationTLSFixture, binary string) nginxactivation.Rotation {
	t.Helper()
	prefix := filepath.Join(f.root, "nginx")
	directory := filepath.Join(prefix, "activation")
	version := filepath.Join(directory, "00000000000000000000000000000001")
	if err := os.MkdirAll(version, 0o700); err != nil {
		t.Fatal(err)
	}
	for destination, source := range map[string]string{"fullchain.pem": f.config.Server.TLSCert, "key.pem": f.config.Server.TLSKey} {
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		writeApplicationTLSFile(t, filepath.Join(version, destination), content)
	}
	include := fmt.Sprintf("ssl_certificate %s/fullchain.pem;\nssl_certificate_key %s/key.pem;\n", version, version)
	writeApplicationTLSFile(t, filepath.Join(directory, "active.conf"), []byte(include))
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
`, prefix, prefix, prefix, prefix, prefix, prefix, f.proxy.Address, f.proxy.Hostname,
		directory, f.upstream.Address, f.config.Web.Hostname, f.config.Web.Hostname, f.caPath)
	configPath := filepath.Join(prefix, "nginx.conf")
	writeApplicationTLSFile(t, configPath, []byte(configuration))
	reloader := nginxactivation.CommandReloader{Binary: binary, Config: configPath, Prefix: prefix + "/"}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := reloader.Check(ctx); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(prefix, "process.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-p", prefix+"/", "-c", configPath, "-g", "daemon off;")
	command.Stdout, command.Stderr = log, log
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGQUIT)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("隔离 Nginx 退出失败：%v", err)
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Error("隔离 Nginx 未按时退出")
		}
		_ = log.Close()
		if t.Failed() {
			content, _ := os.ReadFile(logPath)
			t.Logf("隔离 Nginx 日志：\n%s", content)
		}
	})
	if err := f.proxy.Wait(ctx, f.oldLeaf); err != nil {
		t.Fatal(err)
	}
	return nginxactivation.Rotation{Directory: directory, Proxy: f.proxy, Upstream: f.upstream, Reloader: reloader}
}

func startApplicationActivationIPC(t *testing.T, rotation nginxactivation.Rotation) nginxactivation.Client {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "activation.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	service := nginxactivation.LocalService{Rotation: rotation, AllowedUID: uint32(os.Geteuid())}
	go func() { done <- service.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("隔离激活服务未按时退出")
		}
	})
	return nginxactivation.Client{SocketPath: path, ServiceUID: uint32(os.Geteuid())}
}
