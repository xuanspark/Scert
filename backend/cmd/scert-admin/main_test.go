package main

import (
	"bytes"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
)

func TestSessionInfoDoesNotExposeResetCode(t *testing.T) {
	const secretCode = "ABCDE-12345-FGHIJ"
	var output bytes.Buffer
	writeSessionInfo(&output, admin.Session{
		State: admin.State{
			ResetURL: "https://scert.example.test/reset",
			CodePath: "/run/scert-admin/reset-code",
		},
		Code: secretCode,
	})
	if strings.Contains(output.String(), secretCode) {
		t.Fatal("命令行输出不得包含一次性校验码")
	}
	if !strings.Contains(output.String(), "/run/scert-admin/reset-code") {
		t.Fatal("命令行输出必须提示受保护校验码文件路径")
	}
}
