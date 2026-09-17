package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type realPostgres struct {
	root     string
	bin      string
	cli      string
	port     string
	ca       string
	password string
	admin    *pgx.Conn
}

// 只由显式环境变量启用；自行创建集群，不接受现有数据库连接串。
func startRealPostgres(t *testing.T) *realPostgres {
	t.Helper()
	bin := os.Getenv("SCERT_POSTGRES_TEST_BIN")
	if bin == "" {
		t.Skip("未启用 PostgreSQL 真实集成测试")
	}
	root := os.Getenv("SCERT_POSTGRES_TEST_ROOT")
	cli := os.Getenv("SCERT_POSTGRES_TEST_CLI")
	if !filepath.IsAbs(root) || !filepath.IsAbs(bin) || !filepath.IsAbs(cli) || !isRegularFile(cli) {
		t.Fatal("真实测试要求工具、最新编译的 Scert 和隔离产物目录使用绝对路径")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(root, "scert-postgres-integration-")
	if err != nil {
		t.Fatal(err)
	}
	f := &realPostgres{root: root, bin: bin, cli: cli, password: realPostgresPassword(t)}
	t.Logf("隔离集群及演练产物保留于：%s", root)
	for _, name := range []string{"initdb", "pg_ctl", "pg_dump", "pg_restore"} {
		if !isRegularFile(f.tool(name)) {
			t.Fatalf("缺少官方工具：%s", name)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	passwordFile := filepath.Join(root, "bootstrap-password.txt")
	realPostgresWrite(t, passwordFile, []byte(f.password+"\n"))
	data := filepath.Join(root, "cluster")
	f.control(t, "initdb", "-D", data, "-U", "postgres", "--pwfile="+passwordFile, "--auth=scram-sha-256", "--encoding=UTF8", "--no-locale")
	f.ca = filepath.Join(data, "server.crt")
	realPostgresTLS(t, f.ca, filepath.Join(data, "server.key"))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.port = strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	// 隔离集群只处理小规模串行数据，避免默认缓存挤占共享实机内存。
	settings := "shared_buffers = '16MB'\nwork_mem = '1MB'\nmaintenance_work_mem = '16MB'\nmax_connections = 32\nmax_parallel_workers = 0\nmax_worker_processes = 0\n"
	realPostgresWrite(t, filepath.Join(data, "postgresql.conf"), []byte(settings+"listen_addresses = '127.0.0.1'\nport = "+f.port+"\nssl = on\nssl_cert_file = 'server.crt'\nssl_key_file = 'server.key'\npassword_encryption = 'scram-sha-256'\nunix_socket_directories = ''\n"))
	realPostgresWrite(t, filepath.Join(data, "pg_hba.conf"), []byte("hostssl all all 127.0.0.1/32 scram-sha-256\nhost all all 0.0.0.0/0 reject\nhost all all ::0/0 reject\n"))
	// 在启动前注册停止动作，启动超时也尝试关闭自己的集群；不删除任何产物。
	t.Cleanup(func() {
		if f.admin != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = f.admin.Close(ctx)
		}
		if !isRegularFile(filepath.Join(data, "postmaster.pid")) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, f.tool("pg_ctl"), "-D", data, "-m", "fast", "-w", "-t", "20", "stop")
		cmd.Env = postgresToolEnvironment()
		cmd.WaitDelay = 2 * time.Second
		if err := cmd.Run(); err != nil {
			t.Errorf("停止隔离 PostgreSQL 失败，请检查保留目录：%v", err)
		}
	})
	f.control(t, "pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "30", "start")
	f.admin = f.connect(t, "postgres", "postgres")
	realPostgresExec(t, f.admin, "CREATE ROLE scert_owner LOGIN PASSWORD '"+f.password+"'")
	realPostgresExec(t, f.admin, "CREATE ROLE scert_reader")
	return f
}

func (f *realPostgres) tool(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(f.bin, name)
}

func (f *realPostgres) control(t *testing.T, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.tool(name), args...)
	cmd.Env = postgresToolEnvironment()
	// 后台服务会继承句柄，直接使用空设备，不能让其持有 Go 的输出复制管道。
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	cmd.Stdout, cmd.Stderr = null, null
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		t.Fatalf("隔离集群工具 %s 失败，请检查保留目录：%v", name, err)
	}
}

func (f *realPostgres) runCLI(t *testing.T, configuration string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.cli, append([]string{"--config", configuration}, args...)...)
	cmd.Env = postgresToolEnvironment()
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	if strings.Contains(string(output), f.password) {
		t.Fatal("Scert 命令输出泄漏了测试密码")
	}
	if err != nil {
		return string(output), errors.New(string(output))
	}
	return string(output), nil
}

func (f *realPostgres) dsn(database, username string) string {
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort("127.0.0.1", f.port), Path: "/" + database}
	u.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {f.ca}, "connect_timeout": {"5"}}.Encode()
	if username != "" {
		u.User = url.UserPassword(username, f.password)
	}
	return u.String()
}

func (f *realPostgres) connect(t *testing.T, database, username string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), f.dsn(database, username))
	if err != nil {
		t.Fatal("隔离 PostgreSQL 严格 TLS 连接失败")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(ctx)
	})
	return conn
}

func realPostgresExec(t *testing.T, conn *pgx.Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(t.Context(), query, args...); err != nil {
		t.Fatal("隔离 PostgreSQL 测试数据操作失败")
	}
}

func realPostgresWrite(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func realPostgresPassword(t *testing.T) string {
	t.Helper()
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value[:])
}

func realPostgresTLS(t *testing.T, certificatePath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert integration"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:        true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	realPostgresWrite(t, certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	realPostgresWrite(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}))
}
