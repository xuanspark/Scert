package servicehost

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

var ErrInvalidName = errors.New("服务名称无效")

type Options struct {
	Name           string
	DisplayName    string
	Description    string
	Executable     string
	ConfigPath     string
	WorkDir        string
	User           string
	Group          string
	ReadWritePaths []string
}

func (o Options) Validate() error {
	if err := validateName(o.Name); err != nil {
		return err
	}
	if err := validatePathValue(o.Executable, "服务程序路径"); err != nil {
		return err
	}
	if err := validatePathValue(o.ConfigPath, "配置文件路径"); err != nil {
		return err
	}
	if err := validatePathValue(o.WorkDir, "服务工作目录"); err != nil {
		return err
	}
	if err := validateTextValue(o.DisplayName, "服务显示名称"); err != nil {
		return err
	}
	if err := validateTextValue(o.Description, "服务描述"); err != nil {
		return err
	}
	if (strings.TrimSpace(o.User) == "") != (strings.TrimSpace(o.Group) == "") {
		return errors.New("服务用户和用户组必须同时配置")
	}
	if err := validateIdentity(o.User, "服务用户"); err != nil {
		return err
	}
	if err := validateIdentity(o.Group, "服务用户组"); err != nil {
		return err
	}
	for _, path := range o.ReadWritePaths {
		if err := validatePathValue(path, "服务可写路径"); err != nil {
			return err
		}
	}
	return nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := validateName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	for _, character := range name {
		if !(character == '-' || character == '_' || character == '.' || character == '@' ||
			(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9')) {
			return ErrInvalidName
		}
	}
	return nil
}

func validatePathValue(value, label string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s不能为空", label)
	}
	if filepath.Clean(value) == "." {
		return fmt.Errorf("%s无效", label)
	}
	return validateTextValue(value, label)
}

func validateTextValue(value, label string) error {
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s不能包含控制字符", label)
		}
	}
	return nil
}

func validateIdentity(value, label string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, character := range value {
		if !(character == '-' || character == '_' || character == '.' ||
			(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9')) {
			return fmt.Errorf("%s只能包含字母、数字、点、连字符和下划线", label)
		}
	}
	return nil
}

func (o Options) normalized() (Options, error) {
	if err := o.Validate(); err != nil {
		return Options{}, err
	}
	o.Name = strings.TrimSpace(o.Name)
	o.DisplayName = strings.TrimSpace(o.DisplayName)
	if o.DisplayName == "" {
		o.DisplayName = o.Name
	}
	o.Description = strings.TrimSpace(o.Description)
	if o.Description == "" {
		o.Description = o.DisplayName
	}
	o.Executable = filepath.Clean(strings.TrimSpace(o.Executable))
	o.ConfigPath = filepath.Clean(strings.TrimSpace(o.ConfigPath))
	o.WorkDir = filepath.Clean(strings.TrimSpace(o.WorkDir))
	o.User = strings.TrimSpace(o.User)
	o.Group = strings.TrimSpace(o.Group)
	o.ReadWritePaths = normalizePaths(o.ReadWritePaths)
	return o, nil
}

func normalizePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "." {
			result = append(result, path)
		}
	}
	return result
}

func commandError(action string, err error) error {
	return fmt.Errorf("%s服务失败：%w", action, err)
}
