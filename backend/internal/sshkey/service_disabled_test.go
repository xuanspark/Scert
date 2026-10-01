package sshkey

import (
	"errors"
	"testing"
)

func TestServiceIsPermanentlyDisabled(t *testing.T) {
	service := NewService()
	if service == nil {
		t.Fatal("停用兼容服务不能为空")
	}

	if _, err := service.Create(t.Context(), "test", "ignored", "ignored"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("创建操作未被永久停用：%v", err)
	}
	if _, _, err := service.CreateGenerated(t.Context(), "test"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("生成操作未被永久停用：%v", err)
	}
	if _, err := service.List(t.Context()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("列表操作未被永久停用：%v", err)
	}
	if _, err := service.ValidateReference(t.Context(), "ignored"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("引用校验操作未被永久停用：%v", err)
	}
	if err := service.Delete(t.Context(), "ignored"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("删除操作未被永久停用：%v", err)
	}
	if _, err := service.Reference("ignored"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("引用生成操作未被永久停用：%v", err)
	}
}
