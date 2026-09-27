package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealEventRestoreSafety(t *testing.T) {
	server := mysqltest.New(t)
	for _, scenario := range []string{"incoming_enabled", "incoming_versioned", "incoming_default", "target_enabled", "target_hidden", "disabled_allowed", "scheduler_off", "missing_process", "script_changes_scheduler"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := server.Database(t)
			parsed, err := mysql.ParseDSN(cfg.DSN)
			if err != nil {
				t.Fatal(err)
			}
			account := mysqlStringLiteral(parsed.User) + "@'127.0.0.1'"
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE restore_marker (id INT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
				t.Fatal(err)
			}
			event := "CREATE EVENT scheduled ON SCHEDULE AT '2036-01-01 00:00:00' ON COMPLETION PRESERVE ENABLE DO SELECT 'DISABLE'"
			script := "INSERT INTO restore_marker VALUES (1);\n" + event + ";"
			state, expected := "ON", "事件调度器仍为 ON"
			switch scenario {
			case "incoming_versioned":
				script = "INSERT INTO restore_marker VALUES (1);\n/*!50106 " + event + " */;"
			case "incoming_default":
				script = "INSERT INTO restore_marker VALUES (1);\nCREATE EVENT disable ON SCHEDULE EVERY 1 DAY DO SELECT 1;"
			case "target_enabled", "target_hidden":
				if _, err := db.DB.ExecContext(t.Context(), event); err != nil {
					t.Fatal(err)
				}
				script = "INSERT INTO restore_marker VALUES (1);"
				if scenario == "target_hidden" {
					if _, err := server.Admin().ExecContext(t.Context(), "REVOKE EVENT ON "+quoteMySQLIdentifier(parsed.DBName)+".* FROM "+account); err != nil {
						t.Fatal(err)
					}
					expected = "EVENT"
				}
			case "disabled_allowed":
				script = "INSERT INTO restore_marker VALUES (1);\nCREATE EVENT scheduled ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1;"
				expected = ""
			case "scheduler_off":
				state, expected = "OFF", ""
			case "missing_process":
				if _, err := server.Admin().ExecContext(t.Context(), "REVOKE PROCESS ON *.* FROM "+account); err != nil {
					t.Fatal(err)
				}
				expected = "PROCESS"
			case "script_changes_scheduler":
				script = "INSERT INTO restore_marker VALUES (1);\nSET GLOBAL event_scheduler = ON;"
				expected = "恢复文件不能修改"
			}
			if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = "+state); err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(server.Root(), scenario+"-restore.sql")
			if err := os.WriteFile(input, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			err = restoreMySQL(t.Context(), cfg, input)
			count := "0"
			if expected == "" {
				if err != nil {
					t.Fatal(err)
				}
				count = "1"
			} else if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("恢复前应拒绝且不执行首条语句，期望 %s，得到 %v", expected, err)
			}
			checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM restore_marker", count)
			checkMySQLInteropValue(t, cfg, "SELECT @@GLOBAL.event_scheduler", state)
		})
	}
	if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = OFF"); err != nil {
		t.Fatal(err)
	}
	// OFF 不代表已经执行的事件被终止；实库确认工作线程存在时必须拒绝恢复。
	t.Run("running_worker_after_scheduler_off", func(t *testing.T) {
		cfg := server.Database(t)
		db, err := database.Open(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.DB.ExecContext(t.Context(), "CREATE EVENT waiting_event ON SCHEDULE AT CURRENT_TIMESTAMP + INTERVAL 1 SECOND ON COMPLETION PRESERVE ENABLE DO SELECT SLEEP(4)"); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = ON"); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(8 * time.Second)
		var workers int
		for time.Now().Before(deadline) {
			if err := server.Admin().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND = 'Connect' AND STATE = 'User sleep'").Scan(&workers); err != nil {
				t.Fatal(err)
			}
			if workers > 0 {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if workers == 0 {
			t.Fatal("没有观测到事件工作线程，不能把未覆盖的场景算作通过")
		}
		if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = OFF"); err != nil {
			t.Fatal(err)
		}
		input := filepath.Join(server.Root(), "running-worker.sql")
		if err := os.WriteFile(input, []byte("CREATE TABLE must_not_exist (id INT);"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := restoreMySQL(t.Context(), cfg, input); err == nil || !strings.Contains(err.Error(), "工作线程") {
			t.Fatalf("事件尚未退出时应拒绝：%v", err)
		}
		checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()", "0")
		deadline = time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if err := server.Admin().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND = 'Connect'").Scan(&workers); err != nil {
				t.Fatal(err)
			}
			if workers == 0 {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if workers != 0 {
			t.Fatal("事件线程未在预期时间自然退出")
		}
		if err := restoreMySQL(t.Context(), cfg, input); err != nil {
			t.Fatal(err)
		}
	})
}
