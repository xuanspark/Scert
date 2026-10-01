package cli

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"golang.org/x/crypto/bcrypt"
)

func TestSQLiteBusinessBackupRestore(t *testing.T) {
	fixture := newSQLiteBusinessFixture(t)
	cfg := fixture.config
	previous := configPath
	configPath = fixture.path
	t.Cleanup(func() { configPath = previous })
	expected := snapshotSQLiteBusiness(t, cfg)
	input := filepath.Join(t.TempDir(), "business-backup")
	backup := newBackupCommand()
	var output strings.Builder
	backup.SetOut(&output)
	backup.SetErr(&output)
	backup.SetArgs([]string{"--output", input})
	if err := backup.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := validateBackupManifest(input); err != nil {
		t.Fatal(err)
	}

	// 备份后改变数据库、配置、加密凭据和证书，确保恢复不是空操作。
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{
		"UPDATE settings SET value = '变更后'",
		"UPDATE users SET status = 'disabled', groups_json = '[]'",
		"UPDATE domains SET status = 'failed'",
		"UPDATE servers SET name = '变更后服务器'",
		"DELETE FROM domain_servers",
		"UPDATE tasks SET message = '变更后任务'",
		"DELETE FROM task_attempts",
		"INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('after-backup', 'test', 'test', 'success', '不应进入恢复结果')",
	} {
		if _, err := db.DB.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	changed := cfg
	changed.Server.Port = 19005
	if err := changed.Save(fixture.path); err != nil {
		t.Fatal(err)
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	for reference := range fixture.secrets {
		if err := store.Put(t.Context(), reference, map[string]string{"changed": "不应保留"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(fixture.certificate.CertificatePath, []byte("已替换的证书"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, round := range []string{"first_restore", "repeat_restore"} {
		t.Run(round, func(t *testing.T) {
			restore := newRestoreCommand()
			restore.SetOut(&output)
			restore.SetErr(&output)
			restore.SetArgs([]string{"--input", input})
			if err := restore.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			actual := snapshotSQLiteBusiness(t, cfg)
			if !reflect.DeepEqual(actual, expected) {
				for table, want := range expected {
					if actual[table] != want {
						t.Errorf("恢复后的表或模式不一致：%s", table)
					}
				}
				if len(actual) != len(expected) {
					t.Error("恢复后的表清单数量不一致")
				}
			}
			assertSQLiteBusinessUsable(t, fixture)
			// 同一份备份可重复恢复，读取和恢复均不能修改归档清单。
			if err := validateBackupManifest(input); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertSQLiteBusinessUsable(t *testing.T, fixture sqliteBusinessFixture) {
	t.Helper()
	cfg, err := config.Load(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != fixture.config.Server.Port || cfg.DNS != fixture.config.DNS {
		t.Fatal("服务配置或 DNS 归属未恢复")
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	for reference, want := range fixture.secrets {
		actual, err := store.Get(t.Context(), reference)
		if err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatalf("恢复后的加密凭据无法正确读取：%v", err)
		}
	}
	if _, err := tls.LoadX509KeyPair(fixture.certificate.CertificatePath, fixture.certificate.PrivateKeyPath); err != nil {
		t.Fatalf("恢复后的证书与私钥不可用：%v", err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users := repository.NewSQLUserRepository(db.DB)
	user, err := users.FindByIdentifier(t.Context(), "recovery-owner")
	if err != nil {
		t.Fatal(err)
	}
	if user.Status != "active" || user.Role != "super_admin" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(fixture.password)) != nil {
		t.Fatal("恢复后的用户密码或角色不可用")
	}
	member, err := users.GetUserByID(t.Context(), "member")
	if err != nil || !reflect.DeepEqual(member.Groups, []string{"测试分组"}) {
		t.Fatalf("用户分组未恢复：%v", err)
	}
	domains := repository.NewSQLDomainRepository(db.DB)
	item, err := domains.GetByID(t.Context(), "domain")
	if err != nil || item.Status != "issued" || item.ExpiresAt == nil || !item.ExpiresAt.Equal(fixture.certificate.NotAfter) {
		t.Fatalf("域名和到期时间未恢复：%v", err)
	}
	servers := repository.NewSQLServerRepository(db.DB)
	server, err := servers.GetServerByID(t.Context(), "server")
	if err != nil || server.AuthMethod != deployment.SSHAuthPassword || server.PasswordRef != "" {
		t.Fatalf("服务器认证方式未按密码模式恢复：%v", err)
	}
	tasks := repository.NewSQLTaskRepository(db.DB)
	task, err := tasks.GetTask(t.Context(), "task")
	if err != nil || task.DomainID != item.ID || task.OperatorID != user.ID || task.CertificateVersionID != fixture.certificate.Version {
		t.Fatalf("任务资源关联未恢复：%v", err)
	}
	attempts, err := tasks.ListAttempts(t.Context(), task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "success" {
		t.Fatalf("任务尝试未恢复：%v", err)
	}
	var bindings int
	if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM domain_servers WHERE domain_id = ? AND server_id = ?", item.ID, server.ID).Scan(&bindings); err != nil || bindings != 1 {
		t.Fatalf("域名部署服务器关联未恢复：%v", err)
	}
	result, err := db.DB.ExecContext(t.Context(), "INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('after-restore', 'test', 'test', 'success', '恢复后写入')")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := result.LastInsertId(); err != nil || id != 2 {
		t.Fatalf("审计自增序列未恢复：编号=%d，错误=%v", id, err)
	}
}
