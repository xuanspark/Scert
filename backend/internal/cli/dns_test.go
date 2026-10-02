package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func TestDNSCheckRequiresExplicitWriteConfirmation(t *testing.T) {
	called := false
	command := newDNSCheckCommand(func(context.Context, string, map[string]string) (dnsprovider.AuthorizationReport, error) {
		called = true
		return dnsprovider.AuthorizationReport{}, nil
	})
	command.SetArgs([]string{"--domain", "example.com"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--confirm-dns-write") {
		t.Fatalf("缺少写入确认时必须拒绝：%v", err)
	}
	if called {
		t.Fatal("确认前不得调用云端 API")
	}
}

func TestDNSCheckRejectsProtectedDomainBeforeConfigurationOrVerifier(t *testing.T) {
	oldPath := configPath
	configPath = filepath.Join(t.TempDir(), "missing-scert.json")
	t.Cleanup(func() { configPath = oldPath })

	command := newDNSCheckCommand(func(context.Context, string, map[string]string) (dnsprovider.AuthorizationReport, error) {
		t.Fatal("保护域不得进入云端验证")
		return dnsprovider.AuthorizationReport{}, nil
	})
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetArgs([]string{"--domain", "pcux.cn", "--confirm-dns-write"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "pcux.cn") {
		t.Fatalf("保护域必须在读取配置前被拒绝：%v", err)
	}
}

func TestDNSCommandOnlyExposesConfirmedWriteCheck(t *testing.T) {
	command := newDNSCommand()
	var check *cobra.Command
	for _, child := range command.Commands() {
		if child.Name() == "inspect" {
			t.Fatal("不得暴露会被误解为可签发授权的只读 inspect 命令")
		}
		if child.Name() == "check" {
			check = child
		}
	}
	if check == nil {
		t.Fatal("必须提供 dns check 命令")
	}
	if check.Flag("domain") == nil || check.Flag("confirm-dns-write") == nil {
		t.Fatal("dns check 必须同时要求明确域名和写入确认")
	}
}

func TestDNSCheckReportsLiveResultWithoutPersistingConfiguration(t *testing.T) {
	for _, outcome := range []string{"allowed", "denied", "unknown", "unverified"} {
		t.Run(outcome, func(t *testing.T) {
			cfg := prepareDNSCheckConfig(t)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			command := newDNSCheckCommand(func(ctx context.Context, name string, credentials map[string]string) (dnsprovider.AuthorizationReport, error) {
				calls++
				if name != "example.com" || credentials["access_key_id"] != "test-id" {
					t.Fatal("检查目标或凭据来源不正确")
				}
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("权限请求必须设置超时")
				}
				remaining := time.Until(deadline)
				if remaining < 5*time.Second || remaining > 7*time.Second {
					t.Fatalf("阿里云 CLI 权限验证时限错误：%s", remaining)
				}
				report := dnsprovider.AuthorizationReport{Provider: "aliyun", RequestedDomain: name, Verified: outcome == "allowed"}
				switch outcome {
				case "denied":
					return report, errors.New("Forbidden.NoPermission")
				case "unknown":
					return report, context.DeadlineExceeded
				default:
					return report, nil
				}
			})
			var output bytes.Buffer
			command.SilenceErrors, command.SilenceUsage = true, true
			command.SetOut(&output)
			command.SetArgs([]string{"--domain", "EXAMPLE.COM.", "--confirm-dns-write"})
			err = command.Execute()
			if (err == nil) != (outcome == "allowed") || calls != 1 {
				t.Fatalf("检查结果错误：outcome=%s calls=%d err=%v", outcome, calls, err)
			}
			var report dnsprovider.AuthorizationReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Verified != (outcome == "allowed") {
				t.Fatalf("必须输出可机器读取的真实报告：%s", output.String())
			}
			if strings.Contains(output.String(), "test-secret") || strings.Contains(output.String(), "test-id") {
				t.Fatal("输出不得包含凭据")
			}
			after, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("权限诊断不得回写配置或初始化状态")
			}
			if _, err := os.Stat(cfg.Paths.Data); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("权限诊断不得初始化业务数据库：%v", err)
			}
		})
	}
}

func TestDNSCheckDoesNotCreateMissingMasterKey(t *testing.T) {
	oldPath := configPath
	t.Cleanup(func() { configPath = oldPath })
	cfg := isolatedCLIConfig(t.TempDir())
	cfg.DNS.Provider, cfg.DNS.CredentialRef = "aliyun", "secret://dns/check"
	configPath = filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	command := newDNSCheckCommand(nil)
	command.SetArgs([]string{"--domain", "example.com", "--confirm-dns-write"})
	if err := command.Execute(); err == nil {
		t.Fatal("主密钥缺失必须拒绝")
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Config, "master.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("诊断不得创建新主密钥")
	}
}

func prepareDNSCheckConfig(t *testing.T) config.Config {
	t.Helper()
	oldPath := configPath
	t.Cleanup(func() { configPath = oldPath })
	cfg := isolatedCLIConfig(t.TempDir())
	cfg.DNS.Provider, cfg.DNS.CredentialRef = "aliyun", "secret://dns/check"
	configPath = filepath.Join(cfg.Paths.Config, "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	store, err := secretstore.Open(cfg.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), cfg.DNS.CredentialRef, map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret", "managed_zones": "example.com", "allowed_domains": "example.com"}); err != nil {
		t.Fatal(err)
	}
	return cfg
}
