package cli

import "testing"

func TestMySQLEventStatementSafety(t *testing.T) {
	for _, item := range []struct {
		name, statement string
		active          bool
	}{
		{"default_enabled", "CREATE EVENT task ON SCHEDULE EVERY 1 DAY DO SELECT 1", true},
		{"explicit_enabled", "CREATE DEFINER=`owner`@`host` EVENT task ON SCHEDULE EVERY 1 DAY ENABLE DO SELECT 1", true},
		{"disabled", "CREATE EVENT task ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 'ENABLE'", false},
		{"replica_disabled", "CREATE EVENT task ON SCHEDULE EVERY 1 DAY DISABLE ON SLAVE DO SELECT 1", false},
		{"body_not_status", "CREATE EVENT task ON SCHEDULE EVERY 1 DAY DO SELECT 'DISABLE'", true},
		{"comment_not_status", "CREATE EVENT task ON SCHEDULE EVERY 1 DAY COMMENT 'DISABLE' DO SELECT 1", true},
		{"name_not_status", "CREATE EVENT disable ON SCHEDULE EVERY 1 DAY DO SELECT 1", true},
		{"schema_not_status", "CREATE EVENT disable.task ON SCHEDULE EVERY 1 DAY DO SELECT 1", true},
		{"quoted_name", "CREATE EVENT `DO``DISABLE` ON SCHEDULE EVERY 1 DAY DO SELECT 1", true},
		{"if_not_exists", "CREATE EVENT IF NOT EXISTS disable ON SCHEDULE EVERY 1 DAY DO SELECT 1", true},
		{"definer_keywords", "CREATE DEFINER=event@disable EVENT task ON SCHEDULE EVERY 1 DAY DO SELECT 1", true},
		{"current_user", "CREATE DEFINER=CURRENT_USER() EVENT task ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1", false},
		{"versioned", "/*!50106 CREATE*/ /*!50117 DEFINER=`owner`@`host`*/ /*!50106 EVENT task ON SCHEDULE EVERY 1 DAY DO SELECT 1 */", true},
		{"versioned_disabled", "/*!50106 CREATE*/ /*!50117 DEFINER=`owner`@`host`*/ /*!50106 EVENT task ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1 */", false},
		{"alter_enable", "ALTER EVENT task ENABLE", true},
		{"alter_disable", "ALTER EVENT task DISABLE", false},
		{"alter_rename", "ALTER EVENT task RENAME TO disable", true},
		{"alter_preserves_unknown_state", "ALTER EVENT task DO SELECT 'DISABLE'", true},
		{"drop", "DROP EVENT IF EXISTS task", false},
		{"ordinary_comment", "/* CREATE EVENT task ENABLE */ SELECT 1", false},
		{"line_comment", "-- CREATE EVENT task ENABLE\nCREATE EVENT task ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1", false},
		{"routine", "CREATE DEFINER=`owner`@`host` PROCEDURE task() SELECT 'EVENT DISABLE'", false},
	} {
		t.Run(item.name, func(t *testing.T) {
			for _, mode := range []bool{false, true} {
				if actual := mysqlStatementMayEnableEvent(mysqlEventTokens(item.statement, mode)); actual != item.active {
					t.Fatalf("事件调度识别错误：得到 %v，期望 %v，语句 %s", actual, item.active, item.statement)
				}
			}
		})
	}
}
