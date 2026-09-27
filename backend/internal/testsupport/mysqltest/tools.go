package mysqltest

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// 官方工具仅连接本夹具创建的隔离实例，密码只传入子进程环境，不进入命令行或文件。
func (f *Server) Dump(t *testing.T, cfg config.DatabaseConfig, output string) {
	f.dump(t, cfg, output, nil)
}

func (f *Server) DumpStoredPrograms(t *testing.T, cfg config.DatabaseConfig, output string) {
	f.dump(t, cfg, output, []string{"--routines", "--events"})
}

func (f *Server) dump(t *testing.T, cfg config.DatabaseConfig, output string, extra []string) {
	t.Helper()
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	args := []string{
		"--single-transaction", "--no-tablespaces", "--set-gtid-purged=OFF",
		"--skip-add-locks", "--hex-blob", "--complete-insert", "--skip-comments", "--skip-extended-insert",
	}
	f.runTool(t, "mysqldump", cfg, append(args, extra...), nil, file)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func (f *Server) Import(t *testing.T, cfg config.DatabaseConfig, input string) {
	t.Helper()
	file, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f.runTool(t, "mysql", cfg, []string{
		"--binary-mode", "--batch", "--skip-reconnect", "--local-infile=0",
	}, file, io.Discard)
}

func (f *Server) IsMySQL8() bool { return strings.Contains(f.version, "Ver 8.0.") }

func (f *Server) runTool(t *testing.T, name string, cfg config.DatabaseConfig, args []string, input io.Reader, output io.Writer) {
	t.Helper()
	parsed, err := mysql.ParseDSN(cfg.DSN)
	if err != nil || cfg.Driver != "mysql" {
		t.Fatal("官方工具测试连接无效")
	}
	if parsed.Net != "tcp" || parsed.Addr != "127.0.0.1:"+f.port || parsed.TLSConfig != f.tlsName ||
		parsed.Passwd != f.password || !strings.HasPrefix(parsed.DBName, "scert_test_") || !strings.HasPrefix(parsed.User, "scert_user_") {
		t.Fatal("官方工具只允许连接本次隔离实例的测试数据库")
	}
	binary := filepath.Join(filepath.Dir(f.binary), name)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "--no-defaults", "--version").Output()
	if err != nil {
		t.Fatal("无法读取官方工具版本，不能跳过互操作验证")
	}
	expected := "5.7."
	if f.IsMySQL8() {
		expected = "8.0."
	}
	if !strings.Contains(string(version), expected) {
		t.Fatal("官方工具与本次测试服务端版本系列不一致")
	}
	t.Logf("官方工具：%s", strings.TrimSpace(string(version)))
	base := []string{
		"--no-defaults", "--protocol=TCP", "--host=127.0.0.1", "--port=" + f.port,
		"--user=" + parsed.User, "--ssl-mode=VERIFY_IDENTITY", "--ssl-ca=" + filepath.Join(f.root, "server.crt"),
		"--default-character-set=utf8mb4",
	}
	command := exec.CommandContext(ctx, binary, append(append(base, args...), parsed.DBName)...)
	command.Env = mysqlToolTestEnvironment(f.root, parsed.Passwd)
	command.Stdin, command.Stdout = input, output
	// 错误可能含数据库内容；只报告退出状态，不透传工具的原始错误输出。
	command.Stderr = io.Discard
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		t.Fatalf("官方 %s 执行失败：%v；原始输出已隐藏", name, err)
	}
}

func mysqlToolTestEnvironment(root, password string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "MYSQL") || key == "LANG" || key == "LC_ALL" {
			continue
		}
		env = append(env, entry)
	}
	// --no-defaults 仍可能读取登录文件；指定隔离目录中不存在的文件，避免读取用户凭据。
	return append(env, "MYSQL_PWD="+password, "MYSQL_TEST_LOGIN_FILE="+filepath.Join(root, "no-login.cnf"), "LANG=C", "LC_ALL=C")
}
