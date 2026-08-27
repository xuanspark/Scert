package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/app"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func newServerCommand() *cobra.Command {
	command := &cobra.Command{Use: "server", Short: "管理部署服务器"}
	command.AddCommand(newServerListCommand(), newServerShowCommand(), newServerAddCommand(), newServerEditCommand(), newServerDeleteCommand())
	return command
}

func newServerListCommand() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "查看部署服务器列表", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		items, err := service.Servers.ListServers(cmd.Context())
		if err != nil {
			return err
		}
		return printJSON(cmd, items)
	}}
}

func newServerShowCommand() *cobra.Command {
	var id string
	command := &cobra.Command{Use: "show", Short: "查看部署服务器详情", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		item, err := service.Servers.GetServerByID(cmd.Context(), id)
		if err != nil {
			return err
		}
		return printJSON(cmd, item)
	}}
	command.Flags().StringVar(&id, "id", "", "服务器 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func newServerAddCommand() *cobra.Command {
	var value serverFlags
	command := &cobra.Command{Use: "add", Short: "添加部署服务器", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		item := value.server()
		item.ID = domain.NewID()
		if _, err := service.SSHKeys.ValidateReference(cmd.Context(), item.PrivateKeyRef); err != nil {
			return fmt.Errorf("SSH 私钥引用无效：%w", err)
		}
		if err := service.Servers.CreateServer(cmd.Context(), item); err != nil {
			return err
		}
		return printJSON(cmd, item.Normalize())
	}}
	value.bind(command)
	return command
}

func newServerEditCommand() *cobra.Command {
	var id string
	var value serverFlags
	command := &cobra.Command{Use: "edit", Short: "编辑部署服务器", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		existing, err := service.Servers.GetServerByID(cmd.Context(), id)
		if err != nil {
			return err
		}
		item := value.server()
		item.ID = existing.ID
		item.CreatedAt = existing.CreatedAt
		if _, err := service.SSHKeys.ValidateReference(cmd.Context(), item.PrivateKeyRef); err != nil {
			return fmt.Errorf("SSH 私钥引用无效：%w", err)
		}
		if err := service.Servers.UpdateServer(cmd.Context(), item); err != nil {
			return err
		}
		return printJSON(cmd, item.Normalize())
	}}
	command.Flags().StringVar(&id, "id", "", "服务器 ID")
	_ = command.MarkFlagRequired("id")
	value.bind(command)
	return command
}

func newServerDeleteCommand() *cobra.Command {
	var id string
	command := &cobra.Command{Use: "delete", Short: "删除部署服务器", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		if err := service.Servers.DeleteServer(cmd.Context(), id); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "部署服务器已删除：%s\n", id)
		return nil
	}}
	command.Flags().StringVar(&id, "id", "", "服务器 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func newDomainServersCommand() *cobra.Command {
	command := &cobra.Command{Use: "servers", Short: "管理域名关联的部署服务器"}
	command.AddCommand(newDomainServersListCommand(), newDomainServersSetCommand())
	return command
}

func newDomainServersListCommand() *cobra.Command {
	var domainID string
	command := &cobra.Command{Use: "list", Short: "查看域名关联的部署服务器", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		if _, err := service.Repository.GetByID(cmd.Context(), domainID); err != nil {
			return err
		}
		items, err := service.Servers.ListServersByDomainID(cmd.Context(), domainID)
		if err != nil {
			return err
		}
		return printJSON(cmd, items)
	}}
	command.Flags().StringVar(&domainID, "domain-id", "", "域名 ID")
	_ = command.MarkFlagRequired("domain-id")
	return command
}

func newDomainServersSetCommand() *cobra.Command {
	var domainID string
	var serverIDs []string
	command := &cobra.Command{Use: "set", Short: "替换域名关联的部署服务器", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		if _, err := service.Repository.GetByID(cmd.Context(), domainID); err != nil {
			return err
		}
		if err := service.Servers.ReplaceDomainServers(cmd.Context(), domainID, serverIDs); err != nil {
			return err
		}
		items, err := service.Servers.ListServersByDomainID(cmd.Context(), domainID)
		if err != nil {
			return err
		}
		return printJSON(cmd, items)
	}}
	command.Flags().StringVar(&domainID, "domain-id", "", "域名 ID")
	command.Flags().StringSliceVar(&serverIDs, "server-ids", nil, "服务器 ID 列表；留空表示清除全部关联")
	_ = command.MarkFlagRequired("domain-id")
	return command
}

type serverFlags struct {
	name, address, username, privateKeyRef, fingerprint   string
	certificatePath, privateKeyPath, chainPath, webServer string
	reloadCommand                                         []string
	port                                                  int
}

func (f *serverFlags) bind(command *cobra.Command) {
	command.Flags().StringVar(&f.name, "name", "", "服务器名称")
	command.Flags().StringVar(&f.address, "address", "", "服务器地址")
	command.Flags().IntVar(&f.port, "port", 22, "SSH 端口")
	command.Flags().StringVar(&f.username, "username", "", "SSH 用户")
	command.Flags().StringVar(&f.privateKeyRef, "private-key-ref", "", "SSH 私钥的敏感信息引用")
	command.Flags().StringVar(&f.fingerprint, "host-key-fingerprint", "", "SSH 主机 SHA256 指纹")
	command.Flags().StringVar(&f.certificatePath, "certificate-path", deployment.DefaultCertificatePath, "远程证书文件路径模板")
	command.Flags().StringVar(&f.privateKeyPath, "private-key-path", deployment.DefaultPrivateKeyPath, "远程私钥文件路径模板")
	command.Flags().StringVar(&f.chainPath, "chain-path", deployment.DefaultCertificateChainPath, "远程完整证书链路径模板")
	command.Flags().StringVar(&f.webServer, "web-server", string(deployment.WebServerNginx), "Web 服务类型：nginx、caddy、apache、custom")
	command.Flags().StringSliceVar(&f.reloadCommand, "reload-command", nil, "自定义 reload 命令及参数")
	for _, name := range []string{"name", "address", "username", "private-key-ref", "host-key-fingerprint"} {
		_ = command.MarkFlagRequired(name)
	}
}

func (f serverFlags) server() deployment.Server {
	command := make([]string, 0, len(f.reloadCommand))
	for _, value := range f.reloadCommand {
		if value = strings.TrimSpace(value); value != "" {
			command = append(command, value)
		}
	}
	return deployment.Server{
		Name: f.name, Address: f.address, Port: f.port, Username: f.username,
		PrivateKeyRef: f.privateKeyRef, HostKeyFingerprint: f.fingerprint,
		CertificatePath: f.certificatePath, PrivateKeyPath: f.privateKeyPath,
		CertificateChainPath: f.chainPath, WebServer: deployment.WebServer(f.webServer), ReloadCommand: command,
	}
}

func openApplication(cmd *cobra.Command) (*app.App, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return app.New(cmd.Context(), cfg)
}
