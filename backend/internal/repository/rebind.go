package repository

import (
	"strconv"
	"strings"
)

// Rebind 将内部统一使用的参数占位符转换为目标数据库驱动所需格式。
// 它只处理 SQL 字符串字面量和标识符之外的问号，避免改写业务数据。
func Rebind(driver, query string) string {
	if strings.ToLower(strings.TrimSpace(driver)) != "postgres" || !strings.Contains(query, "?") {
		return query
	}

	var result strings.Builder
	result.Grow(len(query) + 8)
	parameter := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false

	for index := 0; index < len(query); index++ {
		char := query[index]
		switch char {
		case '\'':
			if !inDoubleQuote && !inBacktick {
				if inSingleQuote && index+1 < len(query) && query[index+1] == '\'' {
					result.WriteByte(char)
					index++
					result.WriteByte(query[index])
					continue
				}
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote && !inBacktick {
				inDoubleQuote = !inDoubleQuote
			}
		case '`':
			if !inSingleQuote && !inDoubleQuote {
				inBacktick = !inBacktick
			}
		case '?':
			if !inSingleQuote && !inDoubleQuote && !inBacktick {
				parameter++
				result.WriteByte('$')
				result.WriteString(strconv.Itoa(parameter))
				continue
			}
		}
		result.WriteByte(char)
	}
	return result.String()
}

func repositoryDriver(driver []string) string {
	if len(driver) == 0 {
		return "sqlite"
	}
	value := strings.ToLower(strings.TrimSpace(driver[0]))
	if value == "" {
		return "sqlite"
	}
	return value
}
