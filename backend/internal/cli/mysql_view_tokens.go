package cli

import (
	"fmt"
	"strings"
)

type mysqlViewToken struct {
	text string
	kind byte
}

func (t mysqlViewToken) keyword(value string) bool {
	return t.kind == 'w' && strings.EqualFold(t.text, value)
}

func (t mysqlViewToken) identifier() bool { return t.kind == 'w' || t.kind == 'i' }

// 输入仅限 sql_mode 为空且 sql_quote_show_create=1 的服务端规范定义。
// 保留标识符原文；字符串和注释永远不能成为依赖名称。
func lexMySQLView(definition string) ([]mysqlViewToken, error) {
	var tokens []mysqlViewToken
	for i := 0; i < len(definition); {
		c := definition[i]
		if c <= ' ' {
			i++
			continue
		}
		if c == '#' || (strings.HasPrefix(definition[i:], "--") && i+2 < len(definition) && definition[i+2] <= ' ') {
			for i < len(definition) && definition[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(definition[i:], "/*") {
			end := strings.Index(definition[i+2:], "*/")
			if end < 0 || strings.HasPrefix(definition[i:], "/*!") {
				return nil, fmt.Errorf("MySQL 视图规范定义含未闭合或可执行注释")
			}
			i += end + 4
			continue
		}
		if c == '`' || c == '\'' || c == '"' {
			quote := c
			i++
			var value strings.Builder
			closed := false
			for i < len(definition) {
				c = definition[i]
				i++
				if c == quote {
					if i < len(definition) && definition[i] == quote {
						value.WriteByte(c)
						i++
						continue
					}
					closed = true
					break
				}
				if c == '\\' && quote != '`' && i < len(definition) {
					value.WriteByte(definition[i])
					i++
				} else {
					value.WriteByte(c)
				}
			}
			if !closed {
				return nil, fmt.Errorf("MySQL 视图规范定义含未闭合引用")
			}
			kind := byte('s')
			if quote == '`' {
				kind = 'i'
			}
			tokens = append(tokens, mysqlViewToken{text: value.String(), kind: kind})
			continue
		}
		if mysqlViewWordByte(c) {
			start := i
			for i < len(definition) && mysqlViewWordByte(definition[i]) {
				i++
			}
			tokens = append(tokens, mysqlViewToken{text: definition[start:i], kind: 'w'})
			continue
		}
		tokens = append(tokens, mysqlViewToken{text: string(c), kind: 'p'})
		i++
	}
	return tokens, nil
}

func mysqlViewWordByte(c byte) bool {
	return c >= 128 || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$'
}

func mysqlViewGroupEnd(tokens []mysqlViewToken, start int) (int, error) {
	depth := 0
	for i := start; i < len(tokens); i++ {
		if tokens[i].kind != 'p' {
			continue
		}
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("MySQL 视图规范定义含未闭合括号")
}
