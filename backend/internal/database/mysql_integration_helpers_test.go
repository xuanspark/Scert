package database

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type realMySQL struct {
	root     string
	binary   string
	version  string
	port     string
	password string
	tlsName  string
	admin    *sql.DB
	command  *exec.Cmd
	exit     chan error
	sequence int
}

// 只接受程序目录与产物目录；不接受现有实例连接串，也不读取机器上的默认配置。
func startRealMySQL(t *testing.T) *realMySQL {
	t.Helper()
	bin := os.Getenv("SCERT_MYSQL_TEST_BIN")
	if bin == "" {
		t.Skip("未启用 MySQL 隔离实库测试")
	}
	root := os.Getenv("SCERT_MYSQL_TEST_ROOT")
	if !filepath.IsAbs(bin) || !filepath.IsAbs(root) {
		t.Fatal("MySQL 实库测试要求程序与隔离产物目录使用绝对路径")
	}
	binary := filepath.Join(bin, "mysqld")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--no-defaults", "--version").Output()
	if err != nil {
		t.Fatal("无法读取 MySQL 服务端版本")
	}
	version := string(output)
	if !strings.Contains(version, "Ver 5.7.") && !strings.Contains(version, "Ver 8.0.") {
		t.Fatal("隔离测试仅接受明确支持目标 MySQL 5.7 或 8.0")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err = os.MkdirTemp(root, "scert-mysql-integration-")
	if err != nil {
		t.Fatal(err)
	}
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	f := &realMySQL{root: root, binary: binary, version: version, password: hex.EncodeToString(password)}
	t.Logf("独立 MySQL 程序：%s；产物保留于：%s", strings.TrimSpace(version), root)
	f.tlsName = "scert-mysql-" + filepath.Base(root)
	f.createTLS(t)
	t.Cleanup(func() { mysql.DeregisterTLSConfig(f.tlsName) })
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.port = strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	// 初始化阶段不启动监听；首次启动在开放连接前为所有测试管理账户设随机密码。
	initCtx, initCancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer initCancel()
	args := append(f.baseArgs(), "--initialize-insecure")
	if err := exec.CommandContext(initCtx, binary, args...).Run(); err != nil {
		t.Fatal("初始化独立 MySQL 失败，请检查保留目录中的服务端日志")
	}
	bootstrap := "ALTER USER 'root'@'localhost' IDENTIFIED BY '" + f.password + "';\n" +
		"CREATE USER 'scert_harness'@'127.0.0.1' IDENTIFIED BY '" + f.password + "' REQUIRE SSL;\n" +
		"GRANT ALL PRIVILEGES ON *.* TO 'scert_harness'@'127.0.0.1' WITH GRANT OPTION;\n"
	f.write(t, "bootstrap.sql", []byte(bootstrap))
	defer f.write(t, "bootstrap.sql", nil)
	t.Cleanup(func() { f.stop(t) })
	f.start(t, true)
	// 初始口令文件不进入证据；保留空文件而非删除测试产物。
	f.write(t, "bootstrap.sql", nil)
	return f
}

func (f *realMySQL) baseArgs() []string {
	return []string{"--no-defaults", "--basedir=" + filepath.Dir(filepath.Dir(f.binary)),
		"--datadir=" + filepath.Join(f.root, "data"), "--log-error=" + filepath.Join(f.root, "mysql.log"),
		"--innodb-buffer-pool-size=32M", "--innodb-log-file-size=8M", "--performance-schema=OFF"}
}

func (f *realMySQL) start(t *testing.T, bootstrap bool) {
	t.Helper()
	args := append(f.baseArgs(), "--bind-address=127.0.0.1", "--port="+f.port,
		"--skip-name-resolve", "--require-secure-transport=ON", "--max-connections=20",
		"--ssl-ca="+filepath.Join(f.root, "server.crt"), "--ssl-cert="+filepath.Join(f.root, "server.crt"),
		"--ssl-key="+filepath.Join(f.root, "server.key"), "--local-infile=OFF", "--secure-file-priv=NULL",
		"--character-set-server=utf8mb4", "--collation-server=utf8mb4_unicode_ci")
	if strings.Contains(f.version, "Ver 8.0.") {
		args = append(args, "--mysqlx=OFF")
	}
	if runtime.GOOS != "windows" {
		args = append(args, "--socket="+filepath.Join(f.root, "mysql.sock"))
	}
	if bootstrap {
		args = append(args, "--init-file="+filepath.Join(f.root, "bootstrap.sql"))
	}
	f.command = exec.Command(f.binary, args...)
	if err := f.command.Start(); err != nil {
		f.command = nil
		t.Fatal("启动独立 MySQL 失败")
	}
	f.exit = make(chan error, 1)
	command := f.command
	go func() { f.exit <- command.Wait() }()
	var err error
	f.admin, err = sql.Open("mysql", f.dsn("scert_harness", ""))
	if err != nil {
		t.Fatal("创建独立 MySQL 管理连接失败")
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-f.exit:
			f.command = nil
			t.Fatal("独立 MySQL 已退出，请检查保留日志")
		default:
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		err := f.admin.PingContext(ctx)
		cancel()
		if err == nil {
			var version string
			if err := f.admin.QueryRowContext(t.Context(), "SELECT VERSION()").Scan(&version); err != nil {
				t.Fatal(err)
			}
			t.Logf("实连版本：%s；仅监听 127.0.0.1:%s；严格 TLS", version, f.port)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("独立 MySQL 未在时限内就绪，请检查保留日志；不降级 TLS")
}

func (f *realMySQL) stop(t *testing.T) {
	t.Helper()
	if f.command == nil {
		if f.admin != nil {
			_ = f.admin.Close()
		}
		return
	}
	if f.admin != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = f.admin.ExecContext(ctx, "SHUTDOWN")
		cancel()
		_ = f.admin.Close()
	}
	select {
	case err := <-f.exit:
		if err != nil {
			t.Errorf("独立 MySQL 未正常退出：%v", err)
		}
	case <-time.After(15 * time.Second):
		_ = f.command.Process.Kill()
		<-f.exit
		t.Error("独立 MySQL 停止超时，已终止本测试启动的进程")
	}
	f.command = nil
}

func (f *realMySQL) dsn(user, name string) string {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = user, f.password
	cfg.Net, cfg.Addr = "tcp", net.JoinHostPort("127.0.0.1", f.port)
	cfg.DBName, cfg.TLSConfig = name, f.tlsName
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 3*time.Second, 10*time.Second, 10*time.Second
	cfg.ParseTime = true
	return cfg.FormatDSN()
}

func (f *realMySQL) database(t *testing.T) *Database {
	t.Helper()
	f.sequence++
	name := fmt.Sprintf("scert_test_%d", f.sequence)
	user := fmt.Sprintf("scert_user_%d", f.sequence)
	for _, statement := range []string{
		"CREATE DATABASE " + name + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		"CREATE USER '" + user + "'@'127.0.0.1' IDENTIFIED BY '" + f.password + "' REQUIRE SSL",
		"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, DROP, REFERENCES ON " + name + ".* TO '" + user + "'@'127.0.0.1'",
	} {
		if _, err := f.admin.ExecContext(t.Context(), statement); err != nil {
			t.Fatal("创建隔离数据库及库级账户失败")
		}
	}
	return f.open(t, user, name)
}

func (f *realMySQL) open(t *testing.T, user, name string) *Database {
	t.Helper()
	db, err := Open(t.Context(), config.DatabaseConfig{Driver: "mysql", DSN: f.dsn(user, name), MaxOpenConns: 2, MaxIdleConns: 1})
	if err != nil {
		t.Fatal("Scert 打开隔离数据库失败")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (f *realMySQL) write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *realMySQL) createTLS(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert isolated MySQL"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.write(t, "server.crt", encoded)
	f.write(t, "server.key", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(encoded) {
		t.Fatal("解析隔离测试证书失败")
	}
	if err := mysql.RegisterTLSConfig(f.tlsName, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatal(err)
	}
}
