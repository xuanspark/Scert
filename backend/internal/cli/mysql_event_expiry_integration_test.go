package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

func TestMySQLRealEventExpiry(t *testing.T) {
	server := mysqltest.New(t)
	if _, err := server.Admin().ExecContext(t.Context(), "SET GLOBAL event_scheduler = OFF"); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"not_preserved", "enabled_preserved", "disabled_preserved"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := server.Database(t)
			db, err := database.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			options := "NOT PRESERVE ENABLE"
			if scenario == "enabled_preserved" {
				options = "PRESERVE ENABLE"
			} else if scenario == "disabled_preserved" {
				options = "PRESERVE DISABLE"
			}
			if _, err := db.DB.ExecContext(t.Context(), "CREATE EVENT expiry ON SCHEDULE AT CURRENT_TIMESTAMP + INTERVAL 1 SECOND ON COMPLETION "+options+" DO SELECT 1"); err != nil {
				t.Fatal(err)
			}
			var expired bool
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := db.DB.QueryRowContext(t.Context(), "SELECT EXECUTE_AT < CURRENT_TIMESTAMP FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND EVENT_NAME = 'expiry'").Scan(&expired); err != nil {
					t.Fatal(err)
				}
				if expired {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !expired {
				t.Fatal("未实际观测到事件到期")
			}
			output := filepath.Join(server.Root(), scenario+"-expiry.sql")
			if err := os.WriteFile(output, []byte("existing backup"), 0o600); err != nil {
				t.Fatal(err)
			}
			err = backupMySQLDatabase(t.Context(), cfg, output)
			if scenario == "disabled_preserved" {
				if err != nil {
					t.Fatal(err)
				}
				if err := restoreMySQL(t.Context(), cfg, output); err != nil {
					t.Fatal(err)
				}
				checkMySQLInteropValue(t, cfg, "SELECT STATUS FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND EVENT_NAME = 'expiry'", "DISABLED")
				return
			}
			if err == nil || !strings.Contains(err.Error(), "已到期") {
				t.Fatalf("过期事件无法原样重建时应拒绝备份：%v", err)
			}
			data, err := os.ReadFile(output)
			if err != nil || string(data) != "existing backup" {
				t.Fatal("事件导出失败破坏了已有备份")
			}
			if _, err := os.Stat(output + ".tmp"); !os.IsNotExist(err) {
				t.Fatal("失败后遗留临时备份")
			}
			checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE()", "1")
		})
	}
	for _, scenario := range []string{"dropped_after_create", "disabled_after_create"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := server.Database(t)
			completion := "NOT PRESERVE"
			if scenario == "disabled_after_create" {
				completion = "PRESERVE"
			}
			input := filepath.Join(server.Root(), scenario+".sql")
			script := "CREATE EVENT expired ON SCHEDULE AT '2001-01-01 00:00:00' ON COMPLETION " + completion + " ENABLE DO SELECT 1;\nCREATE TABLE must_not_execute (id INT);"
			if err := os.WriteFile(input, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := restoreMySQL(t.Context(), cfg, input); err == nil || !strings.Contains(err.Error(), "未保留原定义或状态") {
				t.Fatalf("事件被服务器删除或禁用时不能报告成功：%v", err)
			}
			checkMySQLInteropValue(t, cfg, "SELECT CAST(COUNT(*) AS CHAR) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()", "0")
		})
	}
}
