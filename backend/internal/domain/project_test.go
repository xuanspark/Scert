package domain

import (
	"strings"
	"testing"
)

func TestNewProject(t *testing.T) {
	item, err := NewProject("  证书项目  ", "  项目说明\n支持换行  ")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == DefaultProjectID || item.Name != "证书项目" || item.Description != "项目说明\n支持换行" {
		t.Fatalf("项目规范化失败：%+v", item)
	}
	if item.CreatedAt.IsZero() || !item.CreatedAt.Equal(item.UpdatedAt) || item.CreatedAt.Location().String() != "UTC" {
		t.Fatal("项目时间戳未初始化为统一 UTC 时间")
	}
	other, err := NewProject(item.Name, "")
	if err != nil || other.ID == item.ID {
		t.Fatal("独立项目 ID 必须唯一，同名项目不应混淆")
	}
}

func TestProjectValidation(t *testing.T) {
	cases := []struct {
		name  string
		item  Project
		valid bool
	}{
		{"default", Project{ID: DefaultProjectID, Name: "默认项目"}, true},
		{"unicode_limit", Project{ID: "project-1", Name: strings.Repeat("证", 128), Description: strings.Repeat("书", 4096)}, true},
		{"multiline", Project{ID: "project_2", Name: "项目", Description: "一\r\n二\t三"}, true},
		{"empty_id", Project{Name: "项目"}, false},
		{"long_id", Project{ID: strings.Repeat("a", 37), Name: "项目"}, false},
		{"uppercase_id", Project{ID: "Default", Name: "项目"}, false},
		{"unsafe_id", Project{ID: "../a", Name: "项目"}, false},
		{"empty_name", Project{ID: "a", Name: "  "}, false},
		{"long_name", Project{ID: "a", Name: strings.Repeat("证", 129)}, false},
		{"name_control", Project{ID: "a", Name: "证\x00书"}, false},
		{"invalid_name_encoding", Project{ID: "a", Name: string([]byte{0xff})}, false},
		{"long_description", Project{ID: "a", Name: "项目", Description: strings.Repeat("书", 4097)}, false},
		{"description_control", Project{ID: "a", Name: "项目", Description: "证\x00书"}, false},
		{"invalid_description_encoding", Project{ID: "a", Name: "项目", Description: string([]byte{0xff})}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.item.Validate(); (err == nil) != tc.valid {
				t.Fatalf("项目校验结果不符：%v", err)
			}
		})
	}
	if _, err := NewProject(" ", ""); err == nil {
		t.Fatal("构造函数未拒绝空项目名称")
	}
}
