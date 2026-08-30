package passwordpolicy

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MinLength = 8
	MaxLength = 12
)

// Validate 校验管理员密码是否符合策略。
func Validate(password, username string) error {
	return validate(password, username, "管理员密码")
}

// ValidateAccountPassword 校验普通账户密码是否符合策略。
func ValidateAccountPassword(password, username string) error {
	return validate(password, username, "账户密码")
}

func validate(password, username, label string) error {
	length := utf8.RuneCountInString(password)
	if length < MinLength {
		return fmt.Errorf("%s长度不能少于 %d 个字符", label, MinLength)
	}
	if length > MaxLength {
		return fmt.Errorf("%s长度不能超过 %d 个字符", label, MaxLength)
	}
	if strings.EqualFold(password, strings.TrimSpace(username)) {
		return fmt.Errorf("%s不能与用户名相同", label)
	}
	hasLetter := false
	hasDigit := false
	for _, value := range password {
		if unicode.IsControl(value) {
			return fmt.Errorf("%s不能包含控制字符", label)
		}
		hasLetter = hasLetter || unicode.IsLetter(value)
		hasDigit = hasDigit || unicode.IsDigit(value)
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("%s必须同时包含字母和数字", label)
	}
	return nil
}
