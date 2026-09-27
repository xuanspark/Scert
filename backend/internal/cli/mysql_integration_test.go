package cli

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealBackupRestore(t *testing.T) {
	server := mysqltest.New(t)
	t.Run("session_identity", func(t *testing.T) {
		cfg := server.Database(t)
		cfg.MaxIdleConns = 0
		input := filepath.Join(server.Root(), "session.sql")
		script := "CREATE TABLE session_probe (id BIGINT NOT NULL, original_id BIGINT NOT NULL); SET @restore_connection = CONNECTION_ID(); INSERT INTO session_probe VALUES (CONNECTION_ID(), @restore_connection);"
		if err := os.WriteFile(input, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := restoreMySQL(t.Context(), cfg, input); err != nil {
			t.Fatalf("恢复必须保留会话变量：%v", err)
		}
		db, err := database.Open(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var same bool
		if err := db.DB.QueryRowContext(t.Context(), "SELECT id = original_id FROM session_probe").Scan(&same); err != nil || !same {
			t.Fatalf("恢复过程发生连接切换：%v", err)
		}
	})
	for _, scenario := range []struct {
		name string
		idle int
	}{
		{name: "without_idle_connections", idle: 0},
		{name: "with_idle_connections", idle: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := server.Database(t)
			cfg.MaxIdleConns = scenario.idle
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			exec := func(statement string, args ...any) sql.Result {
				t.Helper()
				result, err := db.DB.ExecContext(t.Context(), statement, args...)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			// 子表排在父表前面，恢复需要在同一会话内暂时关闭外键检查。
			exec("CREATE TABLE z_parent (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(100) NOT NULL) ENGINE=InnoDB")
			exec("CREATE TABLE a_child (id BIGINT PRIMARY KEY, parent_id BIGINT NOT NULL, payload BLOB, amount DECIMAL(20,6), optional_value TEXT NULL, created_at DATETIME(6), FOREIGN KEY (parent_id) REFERENCES z_parent(id)) ENGINE=InnoDB")
			label := "证书恢复；单引号'与反斜线\\\n"
			payload := []byte{0, 1, 39, 59, 92, 255}
			exec("INSERT INTO z_parent (id, label) VALUES (7, ?)", label)
			exec("INSERT INTO a_child VALUES (1, 7, ?, 123456789.123456, NULL, '2026-09-27 12:34:56.123456')", payload)
			root := filepath.Join(server.Root(), scenario.name)
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(root, "backup.sql")
			if err := backupMySQLDatabase(t.Context(), cfg, backup); err != nil {
				t.Fatal(err)
			}
			exec("DELETE FROM a_child")
			exec("DELETE FROM z_parent")
			exec("INSERT INTO z_parent (id, label) VALUES (8, 'changed')")
			exec("INSERT INTO a_child (id, parent_id) VALUES (2, 8)")
			if err := restoreMySQL(t.Context(), cfg, backup); err != nil {
				t.Fatalf("恢复真实备份失败：%v", err)
			}
			var gotLabel, amount, created string
			var gotPayload []byte
			var optional sql.NullString
			if err := db.DB.QueryRowContext(t.Context(), "SELECT p.label, c.payload, CAST(c.amount AS CHAR), c.optional_value, DATE_FORMAT(c.created_at, '%Y-%m-%d %H:%i:%s.%f') FROM a_child c JOIN z_parent p ON p.id = c.parent_id WHERE c.id = 1 AND p.id = 7").Scan(&gotLabel, &gotPayload, &amount, &optional, &created); err != nil {
				t.Fatal(err)
			}
			if gotLabel != label || !bytes.Equal(gotPayload, payload) || amount != "123456789.123456" || optional.Valid || created != "2026-09-27 12:34:56.123456" {
				t.Fatal("恢复后的文本、二进制、精确数值、空值或时间不一致")
			}
			id, err := exec("INSERT INTO z_parent (label) VALUES ('next')").LastInsertId()
			if err != nil || id != 8 {
				t.Fatalf("恢复后的自增序列不正确：%d，%v", id, err)
			}
			if _, err := db.DB.ExecContext(t.Context(), "INSERT INTO a_child (id, parent_id) VALUES (99, 999)"); err == nil {
				t.Fatal("恢复后仍须强制检查外键约束")
			}

			t.Run("stop_on_error", func(t *testing.T) {
				input := filepath.Join(root, "invalid.sql")
				// 第一条写入已经成功时，不能隐式重放或继续执行失败后的语句。
				script := "SET FOREIGN_KEY_CHECKS=0; INSERT INTO z_parent (id, label) VALUES (20, 'before'); INSERT INTO absent_table VALUES (1); INSERT INTO z_parent (id, label) VALUES (21, 'after');"
				if err := os.WriteFile(input, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := restoreMySQL(t.Context(), cfg, input); err == nil {
					t.Fatal("无效恢复语句应返回错误")
				}
				var count int
				if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM z_parent WHERE id IN (20, 21)").Scan(&count); err != nil || count != 1 {
					t.Fatalf("失败后不应继续执行或隐式重放：%d，%v", count, err)
				}
			})
			t.Run("cancel_before_restore", func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := restoreMySQL(ctx, cfg, backup); err == nil {
					t.Fatal("已取消的恢复不得执行")
				}
				var count int
				if err := db.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM z_parent WHERE id = 20").Scan(&count); err != nil || count != 1 {
					t.Fatalf("取消恢复不应修改数据：%d，%v", count, err)
				}
			})
		})
	}
}
