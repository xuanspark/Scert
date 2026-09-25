package repository

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
)

// NormalizeUserGroups 将未分类表示为空列表，并统一校验分组数量及名称。
func NormalizeUserGroups(role string, groups []string) ([]string, error) {
	if len(groups) > 32 {
		return nil, errors.New("分组数量不能超过 32 个")
	}
	result := make([]string, 0, len(groups))
	seen := make(map[string]bool)
	for _, name := range groups {
		name = strings.TrimSpace(name)
		if name == "" || name == "默认分组" || len([]rune(name)) > 32 {
			return nil, errors.New("分组名称无效：请输入 1 到 32 个字符，默认分组请保持未分组")
		}
		for _, char := range name {
			if unicode.IsControl(char) {
				return nil, errors.New("分组名称不能包含控制字符")
			}
		}
		if !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
	}
	if strings.EqualFold(strings.TrimSpace(role), UserRoleUser) && len(result) > 1 {
		return nil, errors.New("普通用户只能属于一个分组")
	}
	sort.Strings(result)
	return result, nil
}

func encodeUserGroups(item User) (string, error) {
	groups, err := NormalizeUserGroups(item.Role, item.Groups)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(groups)
	return string(data), err
}
