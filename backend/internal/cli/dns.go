package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

type dnsPermissionVerifier func(context.Context, string, map[string]string) (dnsprovider.AuthorizationReport, error)

func newDNSCommand() *cobra.Command {
	command := &cobra.Command{Use: "dns", Short: "检查指定域名的阿里云 RAM 权限"}
	checker := dnsprovider.NewAliyunAuthorizationChecker()
	command.AddCommand(newDNSCheckCommand(checker.Verify), newDNSInspectCommand(checker.Inspect))
	return command
}

func newDNSInspectCommand(inspect dnsPermissionVerifier) *cobra.Command {
	return newDNSPermissionCommand(inspect, true)
}

// 权限检查不启动应用、续期调度或数据库，也不调用全局域名发现。
func newDNSCheckCommand(verify dnsPermissionVerifier) *cobra.Command {
	return newDNSPermissionCommand(verify, false)
}

func newDNSPermissionCommand(verify dnsPermissionVerifier, readOnly bool) *cobra.Command {
	var requestedDomain string
	var confirmWrite bool
	command := &cobra.Command{
		Use:   "check",
		Short: "对指定域名写入临时 TXT 并精确清理，验证当前权限",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			requestedDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(requestedDomain), "."))
			if !domain.IsValidName(requestedDomain) {
				return errors.New("必须提供有效的待检查域名")
			}
			if !readOnly && !confirmWrite {
				return errors.New("此检查会写入并删除专用临时 TXT 记录；确认已获目标域名测试授权后，请添加 --confirm-dns-write")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.DNS.Provider != dnsprovider.Aliyun || strings.TrimSpace(cfg.DNS.CredentialRef) == "" {
				return errors.New("请先配置阿里云 DNS 凭据引用")
			}
			// 不允许诊断入口在密钥缺失时生成新密钥，避免掩盖原有凭据损坏。
			for _, name := range []string{"master.key", "secrets.json"} {
				info, err := os.Stat(filepath.Join(cfg.Paths.Config, name))
				if err != nil || !info.Mode().IsRegular() {
					return fmt.Errorf("现有凭据文件 %s 不可用，请检查配置目录", name)
				}
			}
			store, err := secretstore.Open(cfg.Paths.Config)
			if err != nil {
				return err
			}
			credentials, err := store.Get(cmd.Context(), cfg.DNS.CredentialRef)
			if err != nil {
				return fmt.Errorf("读取阿里云 DNS 凭据失败：%w", err)
			}
			if !readOnly {
				if err := dnsprovider.ValidateAliyunDomainScope(dnsprovider.Aliyun, []string{requestedDomain}, credentials); err != nil {
					return err
				}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 45*time.Second)
			defer cancel()
			report, verifyErr := verify(ctx, requestedDomain, credentials)
			if err := printJSON(cmd, report); err != nil {
				return err
			}
			if verifyErr != nil {
				return fmt.Errorf("域名权限检查未通过：%w", verifyErr)
			}
			if !readOnly && !report.Verified {
				return errors.New("域名权限检查未通过：未获得写入及清理均成功的证据")
			}
			return nil
		},
	}
	command.Flags().StringVar(&requestedDomain, "domain", "", "明确授权检查的单个域名，不枚举全账号域名")
	if readOnly {
		command.Use = "inspect"
		command.Short = "只读检查指定托管区，不写入 DNS、不证明可写"
	} else {
		command.Flags().BoolVar(&confirmWrite, "confirm-dns-write", false, "确认允许专用临时 TXT 记录写入和精确清理")
	}
	_ = command.MarkFlagRequired("domain")
	return command
}
