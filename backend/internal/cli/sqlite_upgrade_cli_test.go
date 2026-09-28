package cli

import (
	"context"
	"crypto/tls"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"golang.org/x/crypto/bcrypt"
)

// 显式指定当批构建的产品程序；不能用测试子进程或辅助脚本冒充独立 CLI。
func TestSQLiteBusinessUpgradeStandaloneCLI(t *testing.T) {
	binary := os.Getenv("SCERT_SQLITE_TEST_CLI")
	if binary == "" {
		t.Skip("未指定本批构建的 Scert 独立程序")
	}
	if !filepath.IsAbs(binary) || !isRegularFile(binary) {
		t.Fatal("独立 CLI 测试要求有效的产品程序绝对路径")
	}
	fixture := newSQLiteBusinessAssets(t)
	cfg := &fixture.config
	hash, err := bcrypt.GenerateFromPassword([]byte(fixture.password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetupReady = true
	cfg.Admin = config.AdminConfig{Username: "upgrade-owner", Email: "owner@example.test", PasswordHash: string(hash)}
	cfg.DNS.OwnerUserID = "primary-super-admin"
	// 仅构造隔离历史配置，不把预置状态当成真实 DNS 或 Web 初始化证据。
	cfg.DNS.VerificationState = "verified"
	cfg.Web.Hostname = "example.test"
	if err := cfg.Save(fixture.path); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"0001_initial.sql", "0002_domain_options.sql", "0003_deployment.sql"} {
		content, err := os.ReadFile(filepath.Join("..", "database", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB.ExecContext(t.Context(), strings.TrimPrefix(string(content), "\ufeff")); err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO schema_migrations (version) VALUES (?)", index+1); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO settings (key, value) VALUES ('historical', '历史设置')`,
		`INSERT INTO domains (id, name, certificate_type, sans_json, status, dns_provider) VALUES ('domain', 'example.test', 'single', '["example.test"]', 'issued', 'alidns')`,
		`INSERT INTO tasks (id, domain_id, task_type, status, message) VALUES ('task', 'domain', 'deploy', 'success', '历史任务')`,
		`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json) VALUES ('server', '历史服务器', '192.0.2.10', 22, 'deploy', 'secret://ssh/business', 'test-only', '/test/cert.pem', '/test/key.pem', '/test/chain.pem', 'nginx', '["nginx","-s","reload"]')`,
		`INSERT INTO domain_servers (domain_id, server_id) VALUES ('domain', 'server')`,
		`INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('deploy', 'domain', 'domain', 'success', '历史审计')`,
	} {
		if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	historical := snapshotSQLiteCLIState(t, *cfg)
	archive := filepath.Join(t.TempDir(), "historical-backup")
	runSQLiteProductCLI(t, binary, fixture, "backup", "--output", archive)
	runSQLiteProductCLI(t, binary, fixture, "certificate", "list")
	assertSQLiteCLIUpgradeUsable(t, fixture)
	createSQLiteProjectForBackup(t, fixture)
	upgraded := snapshotSQLiteCLIState(t, *cfg)
	currentArchive := filepath.Join(t.TempDir(), "upgraded-backup")
	runSQLiteProductCLI(t, binary, fixture, "backup", "--output", currentArchive)

	for _, scenario := range []struct {
		name    string
		archive string
		want    map[string]string
	}{
		{"historical_backup", archive, historical},
		{"upgraded_backup", currentArchive, upgraded},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			current, err := database.Open(t.Context(), cfg.Database)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := current.DB.ExecContext(t.Context(), "UPDATE projects SET name = ?", "待恢复的项目名称"); err != nil {
				_ = current.Close()
				t.Fatal(err)
			}
			if _, err := current.DB.ExecContext(t.Context(), "UPDATE tasks SET message = '不应保留'; DELETE FROM domain_servers"); err != nil {
				_ = current.Close()
				t.Fatal(err)
			}
			if err := current.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.certificate.CertificatePath, []byte("待恢复证书"), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := secretstore.Open(cfg.Paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(t.Context(), cfg.DNS.CredentialRef, map[string]string{"changed": "待恢复"}); err != nil {
				t.Fatal(err)
			}
			runSQLiteProductCLI(t, binary, fixture, "restore", "--input", scenario.archive)
			if actual := snapshotSQLiteCLIState(t, *cfg); !reflect.DeepEqual(actual, scenario.want) {
				t.Fatal("独立 CLI 未恢复全部原始模式和表内容")
			}
			// 从旧备份恢复后再次走正式应用入口升级，不由测试直接调用 Migrate。
			runSQLiteProductCLI(t, binary, fixture, "certificate", "list")
			assertSQLiteCLIUpgradeUsable(t, fixture)
			if err := validateBackupManifest(scenario.archive); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func createSQLiteProjectForBackup(t *testing.T, fixture sqliteBusinessFixture) {
	t.Helper()
	db, err := database.Open(t.Context(), fixture.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	item := domain.Project{ID: "backup-project", Name: "独立程序备份项目", Description: "恢复时必须保留的项目说明"}
	if err := repository.NewSQLProjectRepository(db.DB).CreateProject(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	domains := repository.NewSQLDomainRepository(db.DB)
	resource, err := domains.GetByID(t.Context(), "domain")
	if err != nil {
		t.Fatal(err)
	}
	resource.ID, resource.Name = "project-domain", "project.example.test"
	resource.ResourceScope = domain.ResourceScope{ProjectID: item.ID, OwnerID: "owner", CreatedBy: "creator", UpdatedBy: "editor"}
	if err := domains.Create(t.Context(), resource); err != nil {
		t.Fatal(err)
	}
	servers := repository.NewSQLServerRepository(db.DB)
	server, err := servers.GetServerByID(t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	server.ID = "project-server"
	server.ResourceScope = resource.ResourceScope
	if err := servers.CreateServer(t.Context(), server); err != nil {
		t.Fatal(err)
	}
	if err := servers.ReplaceDomainServers(t.Context(), resource.ID, []string{server.ID}); err != nil {
		t.Fatal(err)
	}
}

func runSQLiteProductCLI(t *testing.T, binary string, fixture sqliteBusinessFixture, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append([]string{"--config", fixture.path}, args...)...)
	cmd.Dir = fixture.config.Paths.Install
	cmd.Env = make([]string, 0)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "SCERT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	for _, values := range fixture.secrets {
		for _, value := range values {
			if strings.Contains(string(output), value) {
				t.Fatal("独立 CLI 输出包含敏感凭据")
			}
		}
	}
	if strings.Contains(string(output), fixture.password) {
		t.Fatal("独立 CLI 输出包含用户密码")
	}
	if err != nil {
		t.Fatalf("独立 CLI %s 失败：%v：%s", args[0], err, output)
	}
}

func snapshotSQLiteCLIState(t *testing.T, cfg config.Config) map[string]string {
	t.Helper()
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result := map[string]string{"schema": snapshotSQLiteRows(t, db.DB, "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name")}
	rows, err := db.DB.QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range tables {
		result[name] = snapshotSQLiteRows(t, db.DB, `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`" ORDER BY 1`)
	}
	return result
}

func assertSQLiteCLIUpgradeUsable(t *testing.T, fixture sqliteBusinessFixture) {
	t.Helper()
	db, err := database.Open(t.Context(), fixture.config.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, bindings int
	if err := db.DB.QueryRowContext(t.Context(), "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != 11 {
		t.Fatalf("正式应用入口未完成历史升级：%d，%v", version, err)
	}
	project, err := repository.NewSQLProjectRepository(db.DB).GetProject(t.Context(), domain.DefaultProjectID)
	if err != nil || project.Name != "默认项目" {
		t.Fatalf("正式应用入口未保留默认项目：%+v，%v", project, err)
	}
	task, err := repository.NewSQLTaskRepository(db.DB).GetTask(t.Context(), "task")
	if err != nil || task.Message != "历史任务" || task.DomainID != "domain" || task.Status != "success" {
		t.Fatalf("历史任务不可用：%v", err)
	}
	if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM domain_servers WHERE domain_id = 'domain' AND server_id = 'server'").Scan(&bindings); err != nil || bindings != 1 {
		t.Fatalf("历史部署绑定丢失：%v", err)
	}
	for _, statement := range []string{
		`DELETE FROM projects WHERE id = 'default'`,
		`UPDATE domains SET project_id = 'missing' WHERE id = 'domain'`,
		`UPDATE domain_servers SET server_id = 'missing' WHERE domain_id = 'domain'`,
	} {
		if _, err := db.DB.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("恢复后的项目引用保护失效：%s", statement)
		}
	}
	user, err := repository.NewSQLUserRepository(db.DB).FindByIdentifier(t.Context(), "upgrade-owner")
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(fixture.password)) != nil {
		t.Fatalf("升级后用户密码不可用：%v", err)
	}
	store, err := secretstore.Open(fixture.config.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	for reference, want := range fixture.secrets {
		actual, err := store.Get(t.Context(), reference)
		if err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatalf("升级恢复后加密凭据不一致：%v", err)
		}
	}
	if _, err := tls.LoadX509KeyPair(fixture.certificate.CertificatePath, fixture.certificate.PrivateKeyPath); err != nil {
		t.Fatalf("升级恢复后证书与私钥不可用：%v", err)
	}
}
