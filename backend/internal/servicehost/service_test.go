package servicehost

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionsValidate(t *testing.T) {
	base := Options{
		Name:       "Scert",
		Executable: filepath.Join("C:", "scert.exe"),
		ConfigPath: filepath.Join("C:", "scert", "scert.json"),
		WorkDir:    filepath.Join("C:", "scert"),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("有效服务配置不应失败：%v", err)
	}
	for name, mutate := range map[string]func(*Options){
		"名称为空":   func(o *Options) { o.Name = "" },
		"名称含路径":  func(o *Options) { o.Name = "a/b" },
		"程序路径为空": func(o *Options) { o.Executable = "" },
		"配置路径为空": func(o *Options) { o.ConfigPath = "" },
		"工作目录为空": func(o *Options) { o.WorkDir = "" },
	} {
		item := base
		mutate(&item)
		if err := item.Validate(); err == nil {
			t.Errorf("%s时应校验失败", name)
		}
	}
}

func TestOptionsNormalizedDefaultsDisplayText(t *testing.T) {
	item, err := (Options{
		Name: " Scert ", Executable: "scert.exe", ConfigPath: "scert.json", WorkDir: filepath.Join("C:", "scert"),
	}).normalized()
	if err != nil {
		t.Fatalf("规范化服务配置失败：%v", err)
	}
	if item.DisplayName != "Scert" || item.Description != "Scert" {
		t.Fatalf("服务显示信息默认值错误：%+v", item)
	}
	if strings.TrimSpace(item.Executable) == "" {
		t.Fatal("规范化后程序路径不能为空")
	}
}

func TestValidateNameRejectsPathLikeValues(t *testing.T) {
	for _, name := range []string{"", " ", ".", "..", "../Scert", "a/b", `a\b`} {
		if err := validateName(name); err == nil {
			t.Errorf("服务名称 %q 未被拒绝", name)
		}
	}
}

func TestOptionsValidateRejectsUnsafeUnitValues(t *testing.T) {
	base := Options{
		Name: "Scert", Executable: "scert", ConfigPath: "scert.json", WorkDir: "scert",
	}
	cases := []Options{
		{Name: "Scert\nInjected", Executable: base.Executable, ConfigPath: base.ConfigPath, WorkDir: base.WorkDir},
		{Name: base.Name, DisplayName: "Scert\nInjected", Executable: base.Executable, ConfigPath: base.ConfigPath, WorkDir: base.WorkDir},
		{Name: base.Name, Description: "Scert\rInjected", Executable: base.Executable, ConfigPath: base.ConfigPath, WorkDir: base.WorkDir},
		{Name: base.Name, User: "Scert", Executable: base.Executable, ConfigPath: base.ConfigPath, WorkDir: base.WorkDir},
		{Name: base.Name, User: "Scert", Group: "Scert Group", Executable: base.Executable, ConfigPath: base.ConfigPath, WorkDir: base.WorkDir},
	}
	for index, item := range cases {
		if err := item.Validate(); err == nil {
			t.Errorf("不安全的服务配置 %d 未被拒绝", index)
		}
	}
}

func TestOptionsNormalizedCopiesServiceSecuritySettings(t *testing.T) {
	firstPath := filepath.Clean(filepath.Join("C:", "var", "lib", "scert"))
	secondPath := filepath.Clean(filepath.Join("C:", "var", "log", "scert"))
	item, err := (Options{
		Name: " Scert ", Executable: "scert", ConfigPath: "scert.json", WorkDir: "scert",
		User: " Scert ", Group: " Scert ", ReadWritePaths: []string{" " + firstPath + " ", " " + secondPath + " "},
	}).normalized()
	if err != nil {
		t.Fatalf("规范化服务安全配置失败：%v", err)
	}
	if item.User != "Scert" || item.Group != "Scert" {
		t.Fatalf("服务用户配置未规范化：%+v", item)
	}
	if len(item.ReadWritePaths) != 2 || item.ReadWritePaths[0] != firstPath || item.ReadWritePaths[1] != secondPath {
		t.Fatalf("服务可写路径未规范化：%+v", item.ReadWritePaths)
	}
}
