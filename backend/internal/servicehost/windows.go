//go:build windows

package servicehost

import (
	"context"
	"os/exec"
	"strconv"
)

func Install(ctx context.Context, options Options) error {
	o, err := options.normalized()
	if err != nil {
		return err
	}
	binPath := strconv.Quote(o.Executable) + " --config " + strconv.Quote(o.ConfigPath) + " serve"
	if err := runSC(ctx, "create", o.Name, "binPath=", binPath, "start=", "auto", "DisplayName=", o.DisplayName); err != nil {
		return commandError("安装", err)
	}
	if err := runSC(ctx, "description", o.Name, o.Description); err != nil {
		return commandError("写入描述", err)
	}
	return nil
}

func Uninstall(ctx context.Context, name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	if err := runSC(ctx, "stop", name); err != nil {
		// 服务未运行时允许继续删除。
		_ = err
	}
	if err := runSC(ctx, "delete", name); err != nil {
		return commandError("卸载", err)
	}
	return nil
}

func Start(ctx context.Context, name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	if err := runSC(ctx, "start", name); err != nil {
		return commandError("启动", err)
	}
	return nil
}

func Stop(ctx context.Context, name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	if err := runSC(ctx, "stop", name); err != nil {
		return commandError("停止", err)
	}
	return nil
}

func runSC(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "sc.exe", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return &commandFailure{output: string(output), err: err}
	}
	return nil
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
