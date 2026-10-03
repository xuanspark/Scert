package postgrestest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// Server 只管理本次测试创建的隔离集群，不接受已有实例的连接串。
type Server struct {
	root     string
	bin      string
	port     string
	password string
	admin    *sql.DB
	sequence int
}

func New(t *testing.T) *Server {
	t.Helper()
	bin, root := os.Getenv("SCERT_POSTGRES_TEST_BIN"), os.Getenv("SCERT_POSTGRES_TEST_ROOT")
	if bin == "" {
		t.Skip("未启用 PostgreSQL 隔离实库测试")
	}
	if !filepath.IsAbs(bin) || !filepath.IsAbs(root) {
		t.Fatal("PostgreSQL 工具与隔离产物目录必须使用绝对路径")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(root, "scert-postgres-integration-")
	if err != nil {
		t.Fatal(err)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	f := &Server{root: root, bin: bin, password: hex.EncodeToString(secret[:])}
	t.Logf("PostgreSQL 隔离产物保留于：%s", root)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "postgres"), "--version")
	cmd.Env = f.environment()
	version, err := cmd.Output()
	if err != nil || !strings.HasPrefix(string(version), "postgres (PostgreSQL) ") {
		t.Fatal("无法确认隔离 PostgreSQL 版本")
	}
	t.Logf("隔离服务端：%s", strings.TrimSpace(string(version)))
	passwordFile := filepath.Join(root, "bootstrap-password.txt")
	write(t, passwordFile, []byte(f.password+"\n"))
	data := filepath.Join(root, "cluster")
	f.control(t, "initdb", "-D", data, "-U", "postgres", "--pwfile="+passwordFile, "--auth=scram-sha-256", "--encoding=UTF8", "--no-locale")
	f.createTLS(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.port = strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	settings := "shared_buffers = '16MB'\nwork_mem = '1MB'\nmaintenance_work_mem = '16MB'\nmax_connections = 24\nmax_parallel_workers = 0\nmax_worker_processes = 0\n"
	settings += "listen_addresses = '127.0.0.1'\nport = " + f.port + "\nssl = on\nssl_cert_file = 'server.crt'\nssl_key_file = 'server.key'\npassword_encryption = 'scram-sha-256'\nunix_socket_directories = ''\n"
	write(t, filepath.Join(data, "postgresql.conf"), []byte(settings))
	write(t, filepath.Join(data, "pg_hba.conf"), []byte("hostssl all all 127.0.0.1/32 scram-sha-256\nhost all all 0.0.0.0/0 reject\nhost all all ::0/0 reject\n"))
	// 即使启动中途失败，也只停止本测试目录中的集群；不删除产物。
	t.Cleanup(func() {
		if f.admin != nil {
			_ = f.admin.Close()
		}
		if _, err := os.Stat(filepath.Join(data, "postmaster.pid")); os.IsNotExist(err) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(bin, "pg_ctl"), "-D", data, "-m", "fast", "-w", "-t", "20", "stop")
		cmd.Env = f.environment()
		cmd.WaitDelay = 2 * time.Second
		if err := cmd.Run(); err != nil {
			t.Errorf("停止隔离 PostgreSQL 失败：%v", err)
		}
	})
	f.control(t, "pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "30", "start")
	f.admin, err = sql.Open("pgx", f.dsn("postgres", "postgres"))
	if err != nil || f.admin.PingContext(t.Context()) != nil {
		t.Fatal("隔离 PostgreSQL 严格 TLS 管理连接失败")
	}
	return f
}

func (f *Server) Database(t *testing.T) config.DatabaseConfig {
	t.Helper()
	f.sequence++
	name, owner := fmt.Sprintf("scert_test_%d", f.sequence), fmt.Sprintf("scert_owner_%d", f.sequence)
	// 每个用例都有独立数据库和无超级权限的属主，不能访问业务数据库。
	for _, query := range []string{
		"CREATE ROLE " + owner + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '" + f.password + "'",
		"CREATE DATABASE " + name + " OWNER " + owner,
		"REVOKE ALL ON DATABASE " + name + " FROM PUBLIC",
	} {
		if _, err := f.admin.ExecContext(t.Context(), query); err != nil {
			t.Fatal("创建隔离 PostgreSQL 数据库及受限属主失败")
		}
	}
	return config.DatabaseConfig{Driver: "postgres", DSN: f.dsn(name, owner), MaxOpenConns: 8, MaxIdleConns: 2}
}

func (f *Server) dsn(name, user string) string {
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort("127.0.0.1", f.port), Path: "/" + name, User: url.UserPassword(user, f.password)}
	u.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {filepath.Join(f.root, "cluster", "server.crt")}, "connect_timeout": {"5"}}.Encode()
	return u.String()
}

func (f *Server) environment() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "PG") || upper == "PATH" || upper == "LC_ALL" || upper == "LANG" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "LC_ALL=C", "LANG=C")
}

func (f *Server) control(t *testing.T, tool string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(f.bin, tool), args...)
	cmd.Env = f.environment()
	// 后台进程不能继承 Go 输出复制管道，否则父进程会一直等待。
	output, err := os.OpenFile(filepath.Join(f.root, tool+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		t.Fatalf("隔离工具 %s 失败，请检查保留日志：%v", tool, err)
	}
}

func write(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *Server) createTLS(t *testing.T) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert isolated PostgreSQL"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.root, "cluster", "server.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	write(t, filepath.Join(f.root, "cluster", "server.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}
