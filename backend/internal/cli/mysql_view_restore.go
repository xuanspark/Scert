package cli

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type mysqlViewRestoreBlock struct {
	statements []string
	name       string
	definition string
	charset    string
}

type mysqlViewRestorePlan struct {
	statements []string
	boundaries map[int]string
}

func (p mysqlViewRestorePlan) checkContext(ctx context.Context, conn *sql.Conn, index int) error {
	expected, exists := p.boundaries[index]
	if !exists {
		return nil
	}
	var schema, mode, charset string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE(), @@SESSION.sql_mode, @@SESSION.character_set_client").Scan(&schema, &mode, &charset); err != nil {
		return err
	}
	if schema != expected {
		return fmt.Errorf("MySQL 官方视图恢复期间数据库发生切换，拒绝使用其他库的依赖顺序")
	}
	if charset != "utf8mb4" && charset != "utf8mb3" && charset != "utf8" {
		return fmt.Errorf("MySQL 官方视图删除名称的实际客户端编码不是 UTF-8，拒绝调整执行顺序")
	}
	for _, value := range strings.Split(mode, ",") {
		if value == "ANSI_QUOTES" || value == "NO_BACKSLASH_ESCAPES" {
			return fmt.Errorf("MySQL 官方视图恢复的实际 SQL 模式与规范解析模式不一致，拒绝调整执行顺序")
		}
	}
	return nil
}

var mysqlViewDumpSetting = regexp.MustCompile(`(?i)^SET\s+(character_set_client|character_set_results|collation_connection)\s*=\s*([a-z0-9_]+)\s*$`)

// 只调整连续的官方最终视图块；块内 SQL 字节、字符集保存与恢复语句均不改写。
// 不跨越业务语句、未知 SET、USE 或其他 DDL，更不在失败后重放任意 SQL。
// 所有解析和编码检查均在执行恢复文件前完成，出错时不会执行其中的删除语句。
func prepareMySQLViewRestore(ctx context.Context, conn *sql.Conn, statements []string) (mysqlViewRestorePlan, error) {
	plan := mysqlViewRestorePlan{boundaries: make(map[int]string)}
	var schema string
	var lowerCaseNames int
	var prepared []string
	for i := 0; i < len(statements); {
		var blocks []mysqlViewRestoreBlock
		for {
			block, ok := readMySQLViewRestoreBlock(statements[i:])
			if !ok {
				break
			}
			blocks = append(blocks, block)
			i += len(block.statements)
		}
		if len(blocks) == 0 {
			prepared = append(prepared, statements[i])
			i++
			continue
		}
		if schema == "" {
			if err := conn.QueryRowContext(ctx, "SELECT DATABASE(), @@lower_case_table_names").Scan(&schema, &lowerCaseNames); err != nil {
				return plan, err
			}
		}
		views := make([]mysqlView, 0, len(blocks))
		byName := make(map[string]mysqlViewRestoreBlock, len(blocks))
		for _, block := range blocks {
			// 官方文件的 DROP 名称用导出编码，CREATE 使用视图原客户端编码。
			// 交给服务器解码并检查往返，不以本机 UTF-8 猜测 Latin-1 等字节。
			charset := quoteMySQLIdentifier(block.charset)
			var definition string
			var roundTrip []byte
			if err := conn.QueryRowContext(ctx, "SELECT CONVERT(CAST(? AS BINARY) USING "+charset+")", []byte(block.definition)).Scan(&definition); err != nil {
				return plan, fmt.Errorf("解码 MySQL 官方视图定义失败：%w", err)
			}
			if err := conn.QueryRowContext(ctx, "SELECT CAST(CONVERT(? USING "+charset+") AS BINARY)", definition).Scan(&roundTrip); err != nil {
				return plan, err
			}
			if !bytes.Equal(roundTrip, []byte(block.definition)) {
				return plan, fmt.Errorf("MySQL 官方视图定义不能无损解码")
			}
			name, err := mysqlViewCreateName(definition)
			if err != nil || name != block.name {
				return plan, fmt.Errorf("MySQL 官方视图块的删除与创建名称不一致，拒绝调整执行顺序")
			}
			views = append(views, mysqlView{name: name, definition: map[string]string{"Create View": definition}})
			byName[name] = block
		}
		ordered, err := orderMySQLViews(views, schema, lowerCaseNames)
		if err != nil {
			return plan, err
		}
		plan.boundaries[len(prepared)] = schema
		for _, view := range ordered {
			prepared = append(prepared, byName[view.name].statements...)
		}
	}
	plan.statements = prepared
	return plan, nil
}

// mysqldump 最终视图块固定包含删除、六条环境保存/设置、创建及三条恢复语句。
// 严格识别这一闭合结构，不能仅凭 CREATE VIEW 就改变用户脚本的时序。
func readMySQLViewRestoreBlock(statements []string) (mysqlViewRestoreBlock, bool) {
	if len(statements) < 11 {
		return mysqlViewRestoreBlock{}, false
	}
	plain := make([]string, 11)
	for i := range plain {
		var ok bool
		plain[i], ok = unwrapMySQLViewDumpStatement(statements[i])
		if !ok {
			return mysqlViewRestoreBlock{}, false
		}
	}
	tokens, err := lexMySQLView(plain[0])
	if err != nil || len(tokens) != 5 || !tokens[0].keyword("DROP") || !tokens[1].keyword("VIEW") || !tokens[2].keyword("IF") || !tokens[3].keyword("EXISTS") || tokens[4].kind != 'i' {
		return mysqlViewRestoreBlock{}, false
	}
	for i, variable := range []string{"character_set_client", "character_set_results", "collation_connection"} {
		saved := []string{"saved_cs_client", "saved_cs_results", "saved_col_connection"}[i]
		if !mysqlViewDumpAssignment(plain[i+1], "@"+saved, "@@"+variable) || !mysqlViewDumpAssignment(plain[i+8], variable, "@"+saved) {
			return mysqlViewRestoreBlock{}, false
		}
		match := mysqlViewDumpSetting.FindStringSubmatch(plain[i+4])
		if len(match) == 0 || !strings.EqualFold(match[1], variable) {
			return mysqlViewRestoreBlock{}, false
		}
	}
	create, err := lexMySQLView(plain[7])
	if err != nil || len(create) < 4 || !create[0].keyword("CREATE") || !create[1].keyword("ALGORITHM") || create[2].text != "=" {
		return mysqlViewRestoreBlock{}, false
	}
	charset := mysqlViewDumpSetting.FindStringSubmatch(plain[4])[2]
	return mysqlViewRestoreBlock{statements: statements[:11], name: tokens[4].text, definition: plain[7], charset: charset}, true
}

func mysqlViewDumpAssignment(statement, left, right string) bool {
	// 仅允许无字符串和无注释的固定赋值；空白只在词法边界忽略。
	tokens, err := lexMySQLView(statement)
	if err != nil || len(tokens) == 0 || !tokens[0].keyword("SET") {
		return false
	}
	expected, err := lexMySQLView("SET " + left + "=" + right)
	if err != nil || len(expected) != len(tokens) {
		return false
	}
	for i, token := range tokens {
		if token.kind != expected[i].kind || !strings.EqualFold(token.text, expected[i].text) {
			return false
		}
	}
	return true
}

func unwrapMySQLViewDumpStatement(statement string) (string, bool) {
	var plain strings.Builder
	for rest := strings.TrimSpace(statement); rest != ""; {
		if !strings.HasPrefix(rest, "/*!") || len(rest) < 11 {
			return "", false
		}
		version, err := strconv.Atoi(rest[3:8])
		if err != nil || version < 50001 || version > 50013 || rest[8] > ' ' {
			return "", false
		}
		end := strings.Index(rest[9:], "*/")
		if end < 0 {
			return "", false
		}
		end += 9
		plain.WriteString(rest[9:end])
		plain.WriteByte(' ')
		rest = strings.TrimSpace(rest[end+2:])
	}
	return strings.TrimSpace(plain.String()), plain.Len() > 0
}

func mysqlViewCreateName(definition string) (string, error) {
	tokens, err := lexMySQLView(definition)
	if err != nil {
		return "", err
	}
	for i, token := range tokens {
		if token.keyword("VIEW") {
			if i+2 < len(tokens) && tokens[i+1].kind == 'i' && tokens[i+2].keyword("AS") {
				return tokens[i+1].text, nil
			}
			break
		}
	}
	return "", fmt.Errorf("MySQL 官方视图创建名称无法识别")
}
