//go:build linux

package servicehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func Install(ctx context.Context, options Options) error {
	o, err := options.normalized()
	if err != nil {
		return err
	}
	if err := writeUnitFile(o); err != nil {
		return commandError("写入", err)
	}
	if err := runSystemctl(ctx, "daemon-reload"); err != nil {
		return commandError("重新加载", err)
	}
	if err := runSystemctl(ctx, "enable", o.Name); err != nil {
		return commandError("注册", err)
	}
	return nil
}

func Uninstall(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "disable", name); err != nil {
		return commandError("取消注册", err)
	}
	if err := removeUnitFile(name); err != nil {
		return commandError("删除", err)
	}
	if err := runSystemctl(ctx, "daemon-reload"); err != nil {
		return commandError("重新加载", err)
	}
	return nil
}

func Start(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "start", name); err != nil {
		return commandError("启动", err)
	}
	return nil
}

func Stop(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "stop", name); err != nil {
		return commandError("停止", err)
	}
	return nil
}

func runSystemctl(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "systemctl", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return &commandFailure{output: string(output), err: err}
	}
	return nil
}

const systemUnitDirectory = "/etc/systemd/system"

func unitFilePath(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return filepath.Join(systemUnitDirectory, name+".service"), nil
}

func writeUnitFile(options Options) error {
	path, err := unitFilePath(options.Name)
	if err != nil {
		return err
	}
	content, err := renderUnitFile(options)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("服务 unit 文件不能是符号链接")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查服务 unit 文件失败：%w", statErr)
	}
	temporary, err := os.CreateTemp(systemUnitDirectory, ".scert-unit-")
	if err != nil {
		return fmt.Errorf("创建临时 unit 文件失败：%w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("设置 unit 文件权限失败：%w", err)
	}
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入 unit 文件失败：%w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步 unit 文件失败：%w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 unit 文件失败：%w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("替换 unit 文件失败：%w", err)
	}
	return nil
}

func removeUnitFile(name string) error {
	path, err := unitFilePath(name)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查服务 unit 文件失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("服务 unit 文件不能是符号链接")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("删除服务 unit 文件失败：%w", err)
	}
	return nil
}

func renderUnitFile(options Options) (string, error) {
	normalized, err := options.normalized()
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"[Unit]",
		"Description=" + normalized.Description,
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"WorkingDirectory=" + quoteUnitValue(normalized.WorkDir),
		"ExecStart=" + quoteUnitValue(normalized.Executable) + " --config " + quoteUnitValue(normalized.ConfigPath) + " serve",
		"KillSignal=SIGTERM",
		"TimeoutStopSec=30",
		"Restart=on-failure",
		"UMask=0077",
		"NoNewPrivileges=yes",
		"PrivateTmp=yes",
		"ProtectHome=yes",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n"), nil
}

func quoteUnitValue(value string) string {
	return strconv.Quote(value)
}

type commandFailure struct {
	output string
	err    error
}

func (e *commandFailure) Error() string {
	if e.err != nil {
		return e.output + ": " + e.err.Error()
	}
	return e.output
}

func (e *commandFailure) Unwrap() error { return e.err }

func validateName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return ErrInvalidName
	}
	return nil
}

var ErrInvalidName = &commandFailure{output: "服务名称无效"}
