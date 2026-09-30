package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type lockReleaseFailureClient struct {
	*memoryClient
}

func (c *lockReleaseFailureClient) Unlock(context.Context, string) error {
	return errors.New("secret-path lock-release-error")
}

func TestDeploymentLockReleaseFailureOverridesSuccess(t *testing.T) {
	client := &lockReleaseFailureClient{memoryClient: newMemoryClient()}
	server := testServer("unlock")
	report := NewService(fixedClientFactory{client}).Deploy(t.Context(), []Target{{Server: server}}, testArtifact(t))
	if report.Status != "failed" || report.Success != 0 || report.Failed != 1 || report.Results[0].FailureCode != FailureLockRelease {
		t.Fatalf("锁释放失败不得保留成功状态：%+v", report)
	}
	public := PublicResult(report.Results[0])
	if strings.Contains(public.Message, "secret-path") || !strings.Contains(public.Message, "部署锁释放未完成") {
		t.Fatalf("锁释放诊断错误：%+v", public)
	}
}

func TestPublicDeploymentDiagnosticsAreAllowlisted(t *testing.T) {
	for code, expected := range publicFailureMessages {
		t.Run(string(code), func(t *testing.T) {
			raw := Result{ServerID: "server-1", Status: "failed", FailureCode: code, Message: "secret-path private-key remote-output"}
			public := PublicResult(raw)
			if public.Message != expected || public.FailureCode != code || public.ServerID != raw.ServerID {
				t.Fatalf("固定诊断或结果身份丢失：%+v", public)
			}
			if PublicStoredFailureMessage(public.Message) != expected {
				t.Fatal("持久化诊断无法读取")
			}
			if PublicStoredFailureMessage(public.Message+" secret-path") != "证书部署失败" {
				t.Fatal("包含额外敏感内容的诊断不应放行")
			}
		})
	}
	for _, code := range []FailureCode{"", "secret-path", "lock_unavailable\nprivate-key"} {
		public := PublicResult(Result{Status: "failed", FailureCode: code, Message: "无法取得部署锁 /secret-path"})
		if public.FailureCode != "" || public.Message != "证书部署失败" {
			t.Fatalf("未知错误码或原始错误泄露：%+v", public)
		}
	}
	public := PublicResult(Result{Status: "success", FailureCode: "secret-path", Message: "部署完成"})
	if public.FailureCode != "" || public.Message != "部署完成" {
		t.Fatalf("成功结果错误：%+v", public)
	}
}

func TestDeploymentFailureCodesDescribeActualRecoveryBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		code      FailureCode
		configure func(*memoryClient, Server)
	}{
		{"lock", FailureLock, func(c *memoryClient, s Server) { c.files[s.PrivateKeyPath+".scert-lock"] = memoryFile{} }},
		{"upload", FailureUpload, func(c *memoryClient, _ Server) { c.uploadError = ".scert-tmp-" }},
		{"replace", FailureReplace, func(c *memoryClient, _ Server) { c.renameError = ".scert-tmp-" }},
		{"validation", FailureValidation, func(c *memoryClient, _ Server) { c.runErrors = []error{errors.New("secret-path")} }},
		{"reload", FailureRuntimeUnknown, func(c *memoryClient, _ Server) { c.runErrors = []error{nil, errors.New("secret-path")} }},
		{"validation_rollback", FailureRollback, func(c *memoryClient, _ Server) {
			c.runErrors = []error{errors.New("secret-path")}
			c.restoreError = true
		}},
		{"reload_rollback", FailureRollback, func(c *memoryClient, _ Server) {
			c.runErrors = []error{nil, errors.New("secret-path")}
			c.restoreError = true
		}},
		{"cleanup", FailureCleanup, func(c *memoryClient, _ Server) { c.removeError = ".scert-backup-" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := newMemoryClient()
			server := testServer("diagnostics")
			for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
				client.files[path] = memoryFile{data: []byte("original"), mode: 0o600}
			}
			scenario.configure(client, server)
			report := NewService(fixedClientFactory{client}).Deploy(t.Context(), []Target{{Server: server}}, testArtifact(t))
			if report.Status != "failed" || len(report.Results) != 1 || report.Results[0].FailureCode != scenario.code {
				t.Fatalf("失败阶段或恢复边界错误：%+v", report)
			}
			public := PublicResult(report.Results[0])
			if strings.Contains(public.Message, "secret-path") || public.Message == "证书部署失败" {
				t.Fatalf("阶段诊断缺失或泄露底层信息：%+v", public)
			}
		})
	}
}
