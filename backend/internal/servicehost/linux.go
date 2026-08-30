//go:build linux

package servicehost

import (
	"context"
	"os/exec"
)

func Install(ctx context.Context, options Options) error {
	o, err := options.normalized()
	if err != nil {
		return err
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
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	return nil
}

var ErrInvalidName = &commandFailure{output: "服务名称无效"}
