package database

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLRealMigrations(t *testing.T) {
	f := startRealMySQL(t)
	baseline := f.database(t)
	if err := Migrate(t.Context(), baseline.DB, "mysql"); err != nil {
		t.Fatal(err)
	}
	expectedSchema := mysqlSchemaSnapshot(t, baseline.DB)
	if err := baseline.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("transport_security", func(t *testing.T) {
		var required bool
		if err := f.admin.QueryRowContext(t.Context(), "SELECT @@require_secure_transport").Scan(&required); err != nil || !required {
			t.Fatal("服务端必须强制安全传输", err)
		}
		var name, cipher string
		if err := f.admin.QueryRowContext(t.Context(), "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&name, &cipher); err != nil || cipher == "" {
			t.Fatal("实连必须使用 TLS", err)
		}
		encoded, err := os.ReadFile(filepath.Join(f.root, "server.crt"))
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(encoded) {
			t.Fatal("解析测试证书失败")
		}
		wrongName := f.tlsName + "-wrong-host"
		if err := mysql.RegisterTLSConfig(wrongName, &tls.Config{RootCAs: roots, ServerName: "wrong.invalid", MinVersion: tls.VersionTLS12}); err != nil {
			t.Fatal(err)
		}
		defer mysql.DeregisterTLSConfig(wrongName)
		for _, mode := range []string{"false", "true", wrongName} {
			cfg, err := mysql.ParseDSN(f.dsn("scert_harness", ""))
			if err != nil {
				t.Fatal("解析隔离连接参数失败")
			}
			cfg.TLSConfig = mode
			conn, err := sql.Open("mysql", cfg.FormatDSN())
			if err != nil {
				t.Fatal("创建反向校验连接失败")
			}
			err = conn.PingContext(t.Context())
			_ = conn.Close()
			if err == nil {
				t.Fatal("非加密、不可信证书或错误主机名连接被接受")
			}
			var serverError *mysql.MySQLError
			var authorityError x509.UnknownAuthorityError
			var hostnameError x509.HostnameError
			switch mode {
			case "false":
				// 账户 REQUIRE SSL 可先于全局传输检查拒绝认证。
				if !errors.As(err, &serverError) || (serverError.Number != 3159 && serverError.Number != 1045) {
					t.Fatalf("应由服务端安全传输策略拒绝：%T", err)
				}
			case "true":
				if !errors.As(err, &authorityError) {
					t.Fatalf("应因证书不可信拒绝：%T", err)
				}
			default:
				if !errors.As(err, &hostnameError) {
					t.Fatalf("应因主机名不匹配拒绝：%T", err)
				}
			}
		}
	})
	for version := 0; version <= latestMigrationVersion; version++ {
		t.Run(fmt.Sprintf("upgrade_from_%d", version), func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, version)
			mysqlAssertUpgrade(t, db.DB, expectedSchema)
		})
	}
	// 遍历每个 DDL 边界，包括全部 DDL 已落盘但版本号未写入的情况。
	for version := 1; version <= latestMigrationVersion; version++ {
		content, err := migrationFiles.ReadFile(fmt.Sprintf("migrations/mysql/%04d_%s.sql", version, migrationName(version)))
		if err != nil {
			t.Fatal(err)
		}
		statements := migrationStatements(content)
		for count := 1; count <= len(statements); count++ {
			t.Run(fmt.Sprintf("interrupted_version_%d_after_%d", version, count), func(t *testing.T) {
				db := f.database(t)
				mysqlPrepareVersion(t, db.DB, version-1)
				for _, statement := range statements[:count] {
					if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
				mysqlAssertUpgrade(t, db.DB, expectedSchema)
			})
		}
	}
	t.Run("existing_business_rows", func(t *testing.T) {
		db := f.database(t)
		mysqlPrepareVersion(t, db.DB, 6)
		for _, statement := range []string{
			"INSERT INTO domains(id,name,certificate_type,sans_json,status) VALUES ('upgrade-domain','upgrade.invalid','single','[]','pending')",
			"INSERT INTO users(id,username,email,password_hash,role) VALUES ('upgrade-user','upgrade-user','upgrade@example.invalid','hash-placeholder','admin')",
			"INSERT INTO servers(id,name,address,port,username,private_key_ref,host_key_fingerprint,certificate_path,private_key_path,certificate_chain_path,web_server,reload_command_json,validation_command_json) VALUES ('upgrade-server','test-server','127.0.0.1',22,'test','key-ref','fingerprint','/test/cert','/test/key','/test/chain','nginx','[]','[]')",
			"INSERT INTO domain_servers(domain_id,server_id) VALUES ('upgrade-domain','upgrade-server')",
		} {
			if _, err := db.DB.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
		mysqlAssertUpgrade(t, db.DB, expectedSchema)
		var method, status, key, domain string
		if err := db.DB.QueryRowContext(t.Context(), "SELECT s.auth_method,s.ssh_key_status,s.private_key_ref,d.name FROM servers s JOIN domain_servers ds ON ds.server_id=s.id JOIN domains d ON d.id=ds.domain_id WHERE s.id='upgrade-server'").Scan(&method, &status, &key, &domain); err != nil {
			t.Fatal(err)
		}
		if method != "private_key" || status != "not_configured" || key != "key-ref" || domain != "upgrade.invalid" {
			t.Fatal("升级改变了原有业务关联或新增字段默认值")
		}
		var groups sql.NullString
		if err := db.DB.QueryRowContext(t.Context(), "SELECT groups_json FROM users WHERE id='upgrade-user'").Scan(&groups); err != nil || groups.Valid {
			t.Fatal("升级未保留用户及默认分组语义", err)
		}
	})
	t.Run("schema_scoped_account", func(t *testing.T) {
		db := f.database(t)
		if _, err := db.DB.ExecContext(t.Context(), "CREATE DATABASE forbidden_database"); err == nil {
			t.Fatal("测试业务账户不应拥有创建其他库的权限")
		}
		if _, err := db.DB.ExecContext(t.Context(), "SELECT * FROM mysql.user"); err == nil {
			t.Fatal("测试业务账户不应能读取管理账户信息")
		}
	})
	newerHistory := make([]int, latestMigrationVersion+1)
	for index := range newerHistory {
		newerHistory[index] = index + 1
	}
	for _, invalid := range []struct {
		name     string
		versions []int
		message  string
	}{
		{"missing_first", []int{2}, "迁移历史不连续"},
		{"missing_middle", []int{1, 3}, "迁移历史不连续"},
		{"zero", []int{0}, "迁移历史不连续"},
		{"negative", []int{-1}, "迁移历史不连续"},
		{"newer", newerHistory, "高于当前程序支持"},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			db := f.database(t)
			mysqlPrepareVersion(t, db.DB, 0)
			for _, version := range invalid.versions {
				if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
					t.Fatal(err)
				}
			}
			err := Migrate(t.Context(), db.DB, "mysql")
			if err == nil || !strings.Contains(err.Error(), invalid.message) {
				t.Fatalf("应拒绝异常历史：%v", err)
			}
			var count int
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != len(invalid.versions) {
				t.Fatalf("异常历史被改变：%d：%v", count, err)
			}
			if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&count); err != nil || count != 2 {
				t.Fatalf("拒绝迁移时创建了业务表：%d：%v", count, err)
			}
			mysqlAssertPreserved(t, db.DB)
		})
	}
	t.Run("restart_persistence", func(t *testing.T) {
		db := f.database(t)
		mysqlPrepareVersion(t, db.DB, 0)
		mysqlAssertUpgrade(t, db.DB, expectedSchema)
		var name, user string
		if err := db.DB.QueryRowContext(t.Context(), "SELECT DATABASE(), SUBSTRING_INDEX(CURRENT_USER(), '@', 1)").Scan(&name, &user); err != nil {
			t.Fatal(err)
		}
		// 模拟停机重启时重新打开应用连接；不将旧连接池遇到断线等同于数据丢失。
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		f.stop(t)
		f.start(t, false)
		reopened := f.open(t, user, name)
		mysqlAssertUpgrade(t, reopened.DB, expectedSchema)
	})
}

func mysqlPrepareVersion(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	for _, statement := range []string{migrationTableSQL("mysql"), "CREATE TABLE preserved_data (value VARCHAR(100) NOT NULL)", "INSERT INTO preserved_data VALUES ('中文迁移数据')"} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for next := 1; next <= version; next++ {
		if err := applyMigration(t.Context(), db, "mysql", next); err != nil {
			t.Fatal(err)
		}
	}
}

func mysqlAssertUpgrade(t *testing.T, db *sql.DB, expectedSchema []string) {
	t.Helper()
	for repeat := 0; repeat < 2; repeat++ {
		if err := Migrate(t.Context(), db, "mysql"); err != nil {
			t.Fatal(err)
		}
	}
	version, err := currentMigrationVersion(t.Context(), db)
	if err != nil || version != latestMigrationVersion {
		t.Fatalf("迁移版本不完整：%d：%v", version, err)
	}
	mysqlAssertPreserved(t, db)
	if actual := mysqlSchemaSnapshot(t, db); !reflect.DeepEqual(actual, expectedSchema) {
		t.Fatal("升级后的字段、索引、默认值或存储引擎与全新迁移不一致")
	}
	var column string
	if err := db.QueryRowContext(t.Context(), "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='servers' AND column_name='ssh_key_status'").Scan(&column); err != nil {
		t.Fatal("最终业务表结构缺失", err)
	}
}

func mysqlSchemaSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	queries := []string{
		"SELECT table_name,engine,table_collation FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name<>'preserved_data' ORDER BY table_name",
		"SELECT table_name,column_name,ordinal_position,column_type,is_nullable,column_default,extra,character_set_name,collation_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name<>'preserved_data' ORDER BY table_name,ordinal_position",
		"SELECT table_name,index_name,non_unique,seq_in_index,column_name,sub_part,index_type FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name<>'preserved_data' ORDER BY table_name,index_name,seq_in_index",
	}
	var snapshot []string
	for _, query := range queries {
		rows, err := db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			snapshot = append(snapshot, fmt.Sprintf("%#v", values))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(snapshot) == 0 {
		t.Fatal("数据库结构快照不能为空")
	}
	return snapshot
}

func mysqlAssertPreserved(t *testing.T, db *sql.DB) {
	t.Helper()
	var value string
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM preserved_data").Scan(&value); err != nil || value != "中文迁移数据" {
		t.Fatalf("原始数据未保留：%v", err)
	}
}
