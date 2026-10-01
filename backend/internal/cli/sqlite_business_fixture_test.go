package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"golang.org/x/crypto/bcrypt"
)

type sqliteBusinessFixture struct {
	config      config.Config
	path        string
	password    string
	secrets     map[string]map[string]string
	certificate certificate.SavedResource
}

func newSQLiteBusinessAssets(t *testing.T) sqliteBusinessFixture {
	t.Helper()
	root := t.TempDir()
	fixture := sqliteBusinessFixture{config: config.Default(root), password: rand.Text()}
	cfg := &fixture.config
	// 明确覆盖全部路径，Linux 测试也不能读写正式安装目录。
	cfg.Paths = config.PathsConfig{
		Install: root, Config: filepath.Join(root, "conf"), Data: filepath.Join(root, "data"),
		Certificate: filepath.Join(root, "ssl"), Log: filepath.Join(root, "log"), Backup: filepath.Join(root, "backup"),
	}
	cfg.Database.DSN = filepath.Join(cfg.Paths.Data, "scert.db")
	for _, directory := range []string{cfg.Paths.Config, cfg.Paths.Data, cfg.Paths.Certificate, cfg.Paths.Backup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg.DNS.OwnerUserID = "owner"
	cfg.DNS.Provider = "alidns"
	cfg.DNS.CredentialRef = "secret://dns/business"
	fixture.path = filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(fixture.path); err != nil {
		t.Fatal(err)
	}
	fixture.secrets = map[string]map[string]string{
		cfg.DNS.CredentialRef: {"access_key_id": rand.Text(), "access_key_secret": rand.Text()},
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	for reference, values := range fixture.secrets {
		if err := store.Put(t.Context(), reference, values); err != nil {
			t.Fatal(err)
		}
	}
	fixture.certificate = saveSQLiteBusinessCertificate(t, *cfg)
	return fixture
}

func newSQLiteBusinessFixture(t *testing.T) sqliteBusinessFixture {
	t.Helper()
	fixture := newSQLiteBusinessAssets(t)
	cfg := &fixture.config
	hash, err := bcrypt.GenerateFromPassword([]byte(fixture.password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO settings (key, value) VALUES ('business-recovery', ?)`, []any{`{"label":"中文恢复配置","enabled":true}`}},
		{`INSERT INTO users (id, username, email, password_hash, role, groups_json) VALUES ('owner', 'recovery-owner', 'owner@example.test', ?, 'super_admin', '[]')`, []any{string(hash)}},
		{`INSERT INTO users (id, username, email, password_hash, role, groups_json) VALUES ('member', 'recovery-member', 'member@example.test', ?, 'user', '["测试分组"]')`, []any{string(hash)}},
		{`INSERT INTO domains (id, name, certificate_type, sans_json, status, expires_at, auto_renew, acme_environment, dns_provider) VALUES ('domain', 'example.test', 'single', '["example.test"]', 'issued', ?, 1, 'staging', 'alidns')`, []any{fixture.certificate.NotAfter}},
		{`INSERT INTO servers (id, name, address, port, username, private_key_ref, host_key_fingerprint, certificate_path, private_key_path, certificate_chain_path, web_server, reload_command_json, validation_command_json) VALUES ('server', '恢复测试服务器', '192.0.2.10', 22, 'deploy', '', 'test-only-fingerprint', '/test/cert.pem', '/test/key.pem', '/test/chain.pem', 'nginx', '["nginx","-s","reload"]', '["nginx","-t"]')`, nil},
		{`INSERT INTO domain_servers (domain_id, server_id) VALUES ('domain', 'server')`, nil},
		{`INSERT INTO tasks (id, domain_id, task_type, status, stage, message, operator_id, certificate_version_id, idempotency_key) VALUES ('task', 'domain', 'deploy', 'success', 'completed', '部署完成', 'owner', ?, 'recovery-idempotency')`, []any{fixture.certificate.Version}},
		{`INSERT INTO task_attempts (id, task_id, attempt_number, stage, status, message) VALUES ('attempt', 'task', 1, 'deploy', 'success', '证书已推送')`, nil},
		{`INSERT INTO audit_logs (action, resource_type, resource_id, status, message) VALUES ('deploy', 'domain', 'domain', 'success', '恢复测试审计')`, nil},
	}
	for i, statement := range statements {
		if _, err := db.DB.ExecContext(t.Context(), statement.query, statement.args...); err != nil {
			t.Fatalf("写入业务夹具第 %d 条失败：%v", i+1, err)
		}
	}
	return fixture
}

func saveSQLiteBusinessCertificate(t *testing.T, cfg config.Config) certificate.SavedResource {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	template := &x509.Certificate{SerialNumber: big.NewInt(17), DNSNames: []string{"example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	resource := certificate.Resource{Domains: []string{"example.test"}, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}
	store := certificate.FileStore{Root: cfg.Paths.Certificate, BackupRoot: cfg.Paths.Backup}
	saved, err := store.Save(t.Context(), "example.test", resource)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// 比较全部现有表和模式定义，不以行数相同代替关联、字段及迁移数据一致。
func snapshotSQLiteBusiness(t *testing.T, cfg config.Config) map[string]string {
	t.Helper()
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	tables, err := db.DB.QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	result["schema"] = snapshotSQLiteRows(t, db.DB, "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name")
	for _, name := range names {
		result[name] = snapshotSQLiteRows(t, db.DB, `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`" ORDER BY 1`)
	}
	for _, name := range []string{"settings", "users", "domains", "servers", "domain_servers", "tasks", "task_attempts", "audit_logs", "schema_migrations", "sqlite_sequence"} {
		if result[name] == "" || result[name] == "null" {
			t.Fatalf("业务夹具缺少表数据：%s", name)
		}
	}
	return result
}

func snapshotSQLiteRows(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var values [][]any
	for rows.Next() {
		value := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range dest {
			dest[i] = &value[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
