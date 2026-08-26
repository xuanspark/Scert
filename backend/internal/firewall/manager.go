package firewall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

const (
	backendWindows   = "windows"
	backendUFW       = "ufw"
	backendFirewalld = "firewalld"
)

type manager struct {
	goos   string
	runner Runner
}

type execRunner struct{}

func NewManager() Manager {
	return NewManagerForOS(runtime.GOOS, execRunner{})
}

func NewManagerForOS(goos string, runner Runner) Manager {
	if runner == nil {
		runner = execRunner{}
	}
	return &manager{goos: strings.ToLower(strings.TrimSpace(goos)), runner: runner}
}

func (execRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.CombinedOutput()
	if err == nil {
		return CommandResult{Output: output, ExitCode: 0}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return CommandResult{Output: output, ExitCode: exitErr.ExitCode()}, nil
	}
	return CommandResult{Output: output, ExitCode: -1}, err
}

func (m *manager) OpenTCP(ctx context.Context, port int) (Result, error) {
	if err := validatePort(port); err != nil {
		return Result{}, err
	}
	switch m.goos {
	case "windows":
		return m.openWindows(ctx, port)
	case "linux":
		return m.openLinux(ctx, port)
	default:
		return Result{
			Backend: BackendNone, Port: port,
			Message: fmt.Sprintf("当前操作系统 %s 不在自动防火墙配置范围内，已跳过本机规则创建", m.goos),
		}, nil
	}
}

func (m *manager) Rollback(ctx context.Context, result Result) error {
	if !result.Created {
		return nil
	}
	if err := validatePort(result.Port); err != nil {
		return err
	}
	switch result.Backend {
	case backendWindows:
		return m.rollbackWindows(ctx, result)
	case backendUFW:
		return m.rollbackUFW(ctx, result)
	case backendFirewalld:
		return m.rollbackFirewalld(ctx, result)
	default:
		return fmt.Errorf("无法回滚未知防火墙后端：%s", result.Backend)
	}
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("防火墙端口无效：%d", port)
	}
	return nil
}

func ruleName(port int) string {
	return "Scert Web TCP " + strconv.Itoa(port)
}

func commandError(action, name string, result CommandResult, err error) error {
	if err != nil {
		return fmt.Errorf("%s失败，无法执行 %s：%w", action, name, err)
	}
	message := strings.TrimSpace(string(result.Output))
	if message == "" {
		message = fmt.Sprintf("退出码 %d", result.ExitCode)
	}
	return fmt.Errorf("%s失败：%s", action, message)
}
