package passwordpolicy

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MinLength = 8
	MaxLength = 128
)

func Validate(password, username string) error {
	length := utf8.RuneCountInString(password)
	if length < MinLength {
		return fmt.Errorf("超级管理员密码至少需要 %d 个字符", MinLength)
	}
	if length > MaxLength {
		return fmt.Errorf("超级管理员密码最多允许 %d 个字符", MaxLength)
	}
	if strings.EqualFold(password, strings.TrimSpace(username)) {
		return fmt.Errorf("超级管理员密码不能与用户名相同")
	}
	hasLetter := false
	hasDigit := false
	for _, value := range password {
		if unicode.IsControl(value) {
			return fmt.Errorf("超级管理员密码不能包含控制字符")
		}
		hasLetter = hasLetter || unicode.IsLetter(value)
		hasDigit = hasDigit || unicode.IsDigit(value)
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("超级管理员密码必须同时包含字母和数字")
	}
	return nil
}
