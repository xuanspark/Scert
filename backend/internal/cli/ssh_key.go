package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newSSHKeyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-key",
		Short: "SSH 密钥登录功能已停用",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("SSH 密钥登录功能已永久停用，仅支持 SSH 密码认证")
		},
	}
}

func newSSHKeyListCommand() *cobra.Command {
	return newSSHKeyCommand()
}

func newSSHKeyAddCommand() *cobra.Command {
	return newSSHKeyCommand()
}

func newSSHKeyDeleteCommand() *cobra.Command {
	return newSSHKeyCommand()
}
