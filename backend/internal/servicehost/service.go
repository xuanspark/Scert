package servicehost

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Options struct {
	Name        string
	DisplayName string
	Description string
	Executable  string
	ConfigPath  string
	WorkDir     string
}

func (o Options) Validate() error {
	if strings.TrimSpace(o.Name) == "" {
		return errors.New("服务名称不能为空")
	}
	if strings.ContainsAny(o.Name, `/\\`) {
		return errors.New("服务名称不能包含路径分隔符")
	}
	if strings.TrimSpace(o.Executable) == "" {
		return errors.New("服务程序路径不能为空")
	}
	if filepath.Clean(o.Executable) == "." {
		return errors.New("服务程序路径无效")
	}
	if strings.TrimSpace(o.ConfigPath) == "" {
		return errors.New("配置文件路径不能为空")
	}
	if filepath.Clean(o.ConfigPath) == "." {
		return errors.New("配置文件路径无效")
	}
	if strings.TrimSpace(o.WorkDir) == "" {
		return errors.New("服务工作目录不能为空")
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
	return o, nil
}

func commandError(action string, err error) error {
	return fmt.Errorf("%s服务失败：%w", action, err)
}
