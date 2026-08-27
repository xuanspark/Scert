package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/sshkey"
)

func newSSHKeyCommand() *cobra.Command {
	command := &cobra.Command{Use: "ssh-key", Short: "管理 SSH 部署私钥"}
	command.AddCommand(newSSHKeyListCommand(), newSSHKeyAddCommand(), newSSHKeyDeleteCommand())
	return command
}

func newSSHKeyListCommand() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "查看 SSH 私钥元数据", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		items, err := service.SSHKeys.List(cmd.Context())
		if err != nil {
			return err
		}
		return printJSON(cmd, items)
	}}
}

func newSSHKeyAddCommand() *cobra.Command {
	var name, filePath, passphraseFile string
	command := &cobra.Command{Use: "add", Short: "从文件安全录入 SSH 私钥", RunE: func(cmd *cobra.Command, args []string) error {
		privateKey, err := readLimitedFile(filePath, sshkey.MaxPrivateKeyBytes)
		if err != nil {
			return fmt.Errorf("读取 SSH 私钥文件失败：%w", err)
		}
		passphrase := ""
		if strings.TrimSpace(passphraseFile) != "" {
			content, err := readLimitedFile(passphraseFile, 4096)
			if err != nil {
				return fmt.Errorf("读取 SSH 私钥口令文件失败：%w", err)
			}
			passphrase = strings.TrimRight(content, "\r\n")
		}
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		item, err := service.SSHKeys.Create(cmd.Context(), name, privateKey, passphrase)
		if err != nil {
			return err
		}
		return printJSON(cmd, item)
	}}
	command.Flags().StringVar(&name, "name", "", "SSH 私钥名称")
	command.Flags().StringVar(&filePath, "file", "", "SSH 私钥文件路径")
	command.Flags().StringVar(&passphraseFile, "passphrase-file", "", "可选的私钥口令文件路径")
	_ = command.MarkFlagRequired("name")
	_ = command.MarkFlagRequired("file")
	return command
}

func newSSHKeyDeleteCommand() *cobra.Command {
	var id string
	command := &cobra.Command{Use: "delete", Short: "删除未被服务器使用的 SSH 私钥", RunE: func(cmd *cobra.Command, args []string) error {
		service, err := openApplication(cmd)
		if err != nil {
			return err
		}
		defer service.Close()
		reference, err := service.SSHKeys.Reference(id)
		if err != nil {
			return err
		}
		used, err := service.Servers.IsPrivateKeyRefUsed(cmd.Context(), reference)
		if err != nil {
			return err
		}
		if used {
			return errors.New("SSH 私钥仍被部署服务器使用，不能删除")
		}
		if err := service.SSHKeys.Delete(cmd.Context(), id); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "SSH 私钥已删除：%s\n", id)
		return nil
	}}
	command.Flags().StringVar(&id, "id", "", "SSH 私钥 ID")
	_ = command.MarkFlagRequired("id")
	return command
}

func readLimitedFile(path string, limit int) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("文件路径不能为空")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(content) > limit {
		return "", fmt.Errorf("文件不能超过 %d 字节", limit)
	}
	return string(content), nil
}
