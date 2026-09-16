package cli

import (
	"context"
	"errors"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func validateBackupDriver(driver string) error {
	switch driver {
	case "sqlite", "mysql":
		return nil
	case "postgres":
		return errors.New("PostgreSQL 完整备份与恢复尚未实现，拒绝生成或恢复不含数据库的备份；请使用数据库原生工具并单独保护配置、加密凭据和证书")
	default:
		return errors.New("当前数据库类型不支持备份与恢复")
	}
}

// 返回值仅供本次维护操作连接数据库，禁止写入配置、备份清单或日志。
func resolveMaintenanceDatabase(ctx context.Context, cfg config.DatabaseConfig, directory string) (config.DatabaseConfig, error) {
	if err := database.ValidateConfig(cfg); err != nil {
		return config.DatabaseConfig{}, errors.New("数据库维护配置无效，请检查数据库类型与加密凭据引用")
	}
	if cfg.Driver == "sqlite" {
		return cfg, nil
	}
	store, err := secretstore.Open(directory)
	if err != nil {
		return config.DatabaseConfig{}, errors.New("无法打开数据库维护凭据存储，请检查目录及主密钥权限")
	}
	resolved, err := database.ResolveConfig(ctx, cfg, store)
	if err != nil {
		return config.DatabaseConfig{}, errors.New("无法读取数据库维护账号，请检查加密凭据及主密钥")
	}
	return resolved, nil
}
