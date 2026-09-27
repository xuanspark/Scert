package cli

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// 预检必须先于任何恢复语句；不会擅自关闭实例调度器或修改已有事件状态。
// 管理员仍须维持维护窗口，禁止外部会话在预检后开启调度或修改事件。
func preflightMySQLEventRestore(ctx context.Context, conn *sql.Conn, statements []string) error {
	active := false
	for _, statement := range statements {
		// 两种字符串转义模式中任一种无法证明安全，就要求停止调度。
		for _, noBackslash := range []bool{false, true} {
			tokens := mysqlEventTokens(statement, noBackslash)
			if len(tokens) > 0 && tokens[0] == "SET" {
				for _, token := range tokens[1:] {
					if token == "EVENT_SCHEDULER" {
						return fmt.Errorf("MySQL 恢复文件不能修改事件调度器，请由管理员在维护窗口单独管理")
					}
				}
			}
			active = active || mysqlStatementMayEnableEvent(tokens)
		}
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version, scheduler string
	if err := tx.QueryRowContext(ctx, "SELECT VERSION(), @@GLOBAL.event_scheduler").Scan(&version, &scheduler); err != nil {
		return fmt.Errorf("读取 MySQL 事件调度状态失败：%w", err)
	}
	grants, err := readMySQLEffectiveGrants(ctx, tx, version)
	if err != nil {
		return err
	}
	process := false
	for _, grant := range grants {
		match := mysqlPrivilegeGrant.FindStringSubmatch(grant)
		if len(match) != 0 && match[1] == "GRANT" && match[3] == "*" && match[4] == "*" && mysqlHasPrivilege(match[2], "PROCESS") {
			process = true
		}
	}
	if !process {
		return fmt.Errorf("MySQL 恢复需要 PROCESS 权限以确认事件工作线程已退出，不能把不可见线程当成没有运行")
	}
	var workers int
	// 事件工作线程以定义者身份运行，不能按 event_scheduler 用户名筛选。
	// Connect 也可能包含正在建立的普通连接；维护窗口内保守拒绝，不冒险误判为空。
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND = 'Connect'").Scan(&workers); err != nil {
		return fmt.Errorf("读取 MySQL 事件工作线程失败：%w", err)
	}
	if workers != 0 {
		return fmt.Errorf("MySQL 仍有事件工作线程运行，请等待其退出后再恢复；不会自动终止事件")
	}
	if strings.EqualFold(scheduler, "ON") {
		if err := requireMySQLEventVisibility(ctx, tx); err != nil {
			return err
		}
		var enabled int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND STATUS = 'ENABLED'").Scan(&enabled); err != nil {
			return err
		}
		if active || enabled != 0 {
			return fmt.Errorf("MySQL 事件调度器仍为 ON，备份或目标库含启用事件；请管理员在维护窗口停止调度并等待事件工作线程退出后重试")
		}
	}
	return nil
}

func mysqlStatementMayEnableEvent(tokens []string) bool {
	_, active := mysqlEventStatement(tokens)
	return active
}

func mysqlEventStatement(tokens []string) (bool, bool) {
	if len(tokens) < 2 || (tokens[0] != "CREATE" && tokens[0] != "ALTER") {
		return false, false
	}
	i := 1
	// 只识别语句开头的对象类型，不把例程正文、注释或标识符中的 EVENT 当成事件。
	if tokens[i] == "DEFINER" {
		i += 3 // 跳过 DEFINER、等号及账户名称。
		if i < len(tokens) && tokens[i] == "@" {
			i += 2
		} else if i+1 < len(tokens) && tokens[i] == "(" && tokens[i+1] == ")" {
			i += 2
		}
	}
	if i >= len(tokens) || tokens[i] != "EVENT" {
		return false, false
	}
	i++
	if i+2 < len(tokens) && tokens[i] == "IF" && tokens[i+1] == "NOT" && tokens[i+2] == "EXISTS" {
		i += 3
	}
	// EVENT、ENABLE、DISABLE 等可能作为未引用的名称，不能把名称当成状态。
	i = mysqlSkipEventName(tokens, i)
	disabled, depth := false, 0
	for ; i < len(tokens); i++ {
		token := tokens[i]
		switch token {
		case "(":
			depth++
		case ")":
			depth--
		default:
			if depth != 0 {
				continue
			}
			switch token {
			case "RENAME":
				if i+1 < len(tokens) && tokens[i+1] == "TO" {
					i = mysqlSkipEventName(tokens, i+2) - 1
				}
			case "DO":
				return true, !disabled
			case "DISABLE":
				disabled = true
			case "ENABLE":
				disabled = false
			}
		}
	}
	return true, !disabled
}

func checkMySQLEventRestoreWarnings(ctx context.Context, conn *sql.Conn, statement string) error {
	event, active := mysqlEventStatement(mysqlEventTokens(statement, false))
	otherEvent, otherActive := mysqlEventStatement(mysqlEventTokens(statement, true))
	if !event && !otherEvent {
		return nil
	}
	rows, err := conn.QueryContext(ctx, "SHOW WARNINGS")
	if err != nil {
		return fmt.Errorf("读取 MySQL 事件恢复警告失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var level, message string
		var code int
		if err := rows.Scan(&level, &code, &message); err != nil {
			return err
		}
		// 到期自动删除、拒绝修改或改变启停状态，不能算作成功恢复。
		if code == 1588 || code == 1589 || code == 1605 || (code == 1544 && (active || otherActive)) {
			return fmt.Errorf("MySQL 事件恢复未保留原定义或状态（警告 %d）：%s", code, message)
		}
	}
	return rows.Err()
}

func mysqlSkipEventName(tokens []string, start int) int {
	if start >= len(tokens) {
		return start
	}
	start++
	if start+1 < len(tokens) && tokens[start] == "." {
		start += 2
	}
	return start
}

// 只用于调度预检，不执行或改写 SQL。版本化可执行注释按可能执行处理。
func mysqlEventTokens(statement string, noBackslash bool) []string {
	var tokens []string
	for i := 0; i < len(statement); {
		c := statement[i]
		if c <= ' ' {
			i++
			continue
		}
		if c == '#' || (c == '-' && i+2 < len(statement) && statement[i+1] == '-' && statement[i+2] <= ' ') {
			for i < len(statement) && statement[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(statement) && statement[i+1] == '*' {
			end := strings.Index(statement[i+2:], "*/")
			if end < 0 {
				return append(tokens, "?")
			}
			end += i + 2
			if i+2 < end && statement[i+2] == '!' {
				start := i + 3
				for start < end && statement[start] >= '0' && statement[start] <= '9' {
					start++
				}
				tokens = append(tokens, mysqlEventTokens(statement[start:end], noBackslash)...)
			}
			i = end + 2
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			i++
			for i < len(statement) {
				if statement[i] == '\\' && c != '`' && !noBackslash {
					i += 2
					continue
				}
				if statement[i] == c {
					i++
					if i < len(statement) && statement[i] == c {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, "?")
			continue
		}
		start := i
		for i < len(statement) && ((statement[i] >= 'a' && statement[i] <= 'z') || (statement[i] >= 'A' && statement[i] <= 'Z') || (statement[i] >= '0' && statement[i] <= '9') || statement[i] == '_' || statement[i] >= 128) {
			i++
		}
		if i == start {
			i++
		}
		tokens = append(tokens, strings.ToUpper(statement[start:i]))
	}
	return tokens
}
