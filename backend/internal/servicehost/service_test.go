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
