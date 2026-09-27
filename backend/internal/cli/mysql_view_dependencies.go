package cli

import (
	"fmt"
	"maps"
	"strings"
)

func orderMySQLViews(views []mysqlView, schema string, lowerCaseNames int) ([]mysqlView, error) {
	normalize := func(value string) string {
		if lowerCaseNames != 0 {
			return strings.ToLower(value)
		}
		return value
	}
	index := make(map[string]int, len(views))
	for i, view := range views {
		key := normalize(view.name)
		if _, exists := index[key]; exists {
			return nil, fmt.Errorf("MySQL 视图名称重复：%s", view.name)
		}
		index[key] = i
	}
	dependencies := make([][]int, len(views))
	for i, view := range views {
		tokens, err := lexMySQLView(view.definition["Create View"])
		if err != nil {
			return nil, fmt.Errorf("解析 MySQL 视图失败（%s）：%w", view.name, err)
		}
		// 从 VIEW 之后的 AS 开始分析，不把定义者或可选列清单当作查询。
		seenView, start := false, -1
		for j, token := range tokens {
			seenView = seenView || token.keyword("VIEW")
			if seenView && token.keyword("AS") {
				start = j + 1
				break
			}
		}
		if start < 0 || start >= len(tokens) {
			return nil, fmt.Errorf("MySQL 视图查询定义不完整：%s", view.name)
		}
		seen := make(map[int]bool)
		add := func(database, name string) {
			if database != "" && normalize(database) != normalize(schema) {
				return
			}
			if target, ok := index[normalize(name)]; ok && !seen[target] {
				seen[target] = true
				dependencies[i] = append(dependencies[i], target)
			}
		}
		if err := walkMySQLViewQuery(tokens[start:], nil, false, 0, add); err != nil {
			return nil, fmt.Errorf("解析 MySQL 视图依赖失败（%s）：%w", view.name, err)
		}
	}
	state := make([]byte, len(views))
	ordered := make([]mysqlView, 0, len(views))
	var visit func(int) error
	visit = func(i int) error {
		if state[i] == 2 {
			return nil
		}
		if state[i] == 1 {
			return fmt.Errorf("MySQL 视图依赖存在循环：%s", views[i].name)
		}
		state[i] = 1
		for _, dependency := range dependencies[i] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[i] = 2
		ordered = append(ordered, views[i])
		return nil
	}
	for i := range views {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// 只在查询块的 FROM/JOIN 表位置记录依赖；递归分析子查询和括号连接。
// TRIM/EXTRACT 中的 FROM、列名、别名和函数名都不是表位置。
func walkMySQLViewQuery(tokens []mysqlViewToken, inherited map[string]bool, tableGroup bool, depth int, add func(string, string)) error {
	if depth > 256 {
		return fmt.Errorf("MySQL 视图查询嵌套过深")
	}
	ctes := maps.Clone(inherited)
	if ctes == nil {
		ctes = make(map[string]bool)
	}
	i := 0
	if len(tokens) > 0 && tokens[0].keyword("WITH") {
		i++
		recursive := i < len(tokens) && tokens[i].keyword("RECURSIVE")
		if recursive {
			i++
		}
		for {
			if i >= len(tokens) || !tokens[i].identifier() {
				return fmt.Errorf("MySQL 视图公共表表达式名称缺失")
			}
			name := strings.ToLower(tokens[i].text)
			i++
			if i < len(tokens) && tokens[i].text == "(" && tokens[i].kind == 'p' {
				end, err := mysqlViewGroupEnd(tokens, i)
				if err != nil {
					return err
				}
				i = end + 1
			}
			if i+1 >= len(tokens) || !tokens[i].keyword("AS") || tokens[i+1].text != "(" || tokens[i+1].kind != 'p' {
				return fmt.Errorf("MySQL 视图公共表表达式定义不完整")
			}
			i++
			end, err := mysqlViewGroupEnd(tokens, i)
			if err != nil {
				return err
			}
			if recursive {
				ctes[name] = true
			}
			if err := walkMySQLViewQuery(tokens[i+1:end], ctes, false, depth+1, add); err != nil {
				return err
			}
			ctes[name] = true
			i = end + 1
			if i >= len(tokens) || tokens[i].kind != 'p' || tokens[i].text != "," {
				break
			}
			i++
		}
	}
	selectSeen, inFrom, expectTable := false, tableGroup, tableGroup
	for ; i < len(tokens); i++ {
		token := tokens[i]
		if token.kind == 'p' && token.text == "(" {
			end, err := mysqlViewGroupEnd(tokens, i)
			if err != nil {
				return err
			}
			if err := walkMySQLViewQuery(tokens[i+1:end], ctes, expectTable, depth+1, add); err != nil {
				return err
			}
			i, expectTable = end, false
			continue
		}
		if token.kind == 'p' && token.text == ")" {
			return fmt.Errorf("MySQL 视图查询括号不匹配")
		}
		switch {
		case token.keyword("SELECT"):
			selectSeen, inFrom, expectTable = true, false, false
		case token.keyword("FROM") && selectSeen:
			inFrom, expectTable = true, true
		case (token.keyword("JOIN") || token.keyword("STRAIGHT_JOIN")) && inFrom:
			expectTable = true
		case token.kind == 'p' && token.text == "," && inFrom:
			expectTable = true
		case token.kind == 'w' && mysqlViewFromBoundary(token.text):
			inFrom, expectTable = false, false
		case expectTable:
			if token.keyword("LATERAL") {
				continue
			}
			if !token.identifier() {
				return fmt.Errorf("MySQL 视图表引用格式无法识别")
			}
			database, name := "", token.text
			if i+1 < len(tokens) && tokens[i+1].kind == 'p' && tokens[i+1].text == "." {
				if i+2 >= len(tokens) || !tokens[i+2].identifier() {
					return fmt.Errorf("MySQL 视图限定表名不完整")
				}
				database, name = name, tokens[i+2].text
				i += 2
			}
			function := i+1 < len(tokens) && tokens[i+1].kind == 'p' && tokens[i+1].text == "("
			if !function && (database != "" || !ctes[strings.ToLower(name)]) {
				add(database, name)
			}
			expectTable = false
		}
	}
	if expectTable {
		return fmt.Errorf("MySQL 视图表引用缺失")
	}
	return nil
}

func mysqlViewFromBoundary(word string) bool {
	switch strings.ToUpper(word) {
	case "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "UNION", "EXCEPT", "INTERSECT", "WINDOW", "INTO", "FOR", "LOCK":
		return true
	}
	return false
}
