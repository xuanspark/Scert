package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"git.pcux.cn/xuans/Scert/backend/internal/servicehost"
)

func newServiceCommand() *cobra.Command {
	var name string
	var displayName string
	var description string
	var executable string
	var serviceConfig string
	var workDir string

	command := &cobra.Command{
		Use:   "service",
		Short: "管理系统服务",
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "注册 Scert 系统服务",
		RunE: func(cmd *cobra.Command, _ []string) error {
			executablePath, directory, configPath, err := servicePaths(executable, workDir, serviceConfig)
			if err != nil {
				return err
			}
			return servicehost.Install(cmd.Context(), servicehost.Options{
				Name: name, DisplayName: displayName, Description: description,
				Executable: executablePath, ConfigPath: configPath, WorkDir: directory,
			})
		},
	}
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "取消注册 Scert 系统服务",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return servicehost.Uninstall(cmd.Context(), name)
		},
	}
	start := &cobra.Command{
		Use:   "start",
		Short: "启动 Scert 系统服务",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return servicehost.Start(cmd.Context(), name)
		},
	}
	stop := &cobra.Command{
		Use:   "stop",
		Short: "停止 Scert 系统服务",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return servicehost.Stop(cmd.Context(), name)
		},
	}
	for _, child := range []*cobra.Command{install, uninstall, start, stop} {
		command.AddCommand(child)
	}
	command.PersistentFlags().StringVar(&name, "name", "Scert", "系统服务名称")
	install.Flags().StringVar(&displayName, "display-name", "Scert SSL 证书管理服务", "系统服务显示名称")
	install.Flags().StringVar(&description, "description", "Scert SSL 证书管理服务", "系统服务描述")
	install.Flags().StringVar(&executable, "executable", "", "服务程序路径，默认使用当前 scert 程序")
	install.Flags().StringVar(&serviceConfig, "service-config", "", "服务配置文件路径，默认使用当前配置")
	install.Flags().StringVar(&workDir, "work-dir", "", "服务工作目录，默认使用程序目录")
	return command
}

func servicePaths(executable, workDir, serviceConfig string) (string, string, string, error) {
	if executable == "" {
		current, err := os.Executable()
		if err != nil {
			return "", "", "", fmt.Errorf("读取当前程序路径失败：%w", err)
		}
		executable = current
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		return "", "", "", fmt.Errorf("解析服务程序路径失败：%w", err)
	}
	if workDir == "" {
		workDir = filepath.Dir(executable)
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return "", "", "", fmt.Errorf("解析服务工作目录失败：%w", err)
	}
	if serviceConfig == "" {
		serviceConfig = resolveConfigPath()
	}
	serviceConfig, err = filepath.Abs(serviceConfig)
	if err != nil {
		return "", "", "", fmt.Errorf("解析服务配置路径失败：%w", err)
	}
	return executable, workDir, serviceConfig, nil
}

var _ = context.Background
