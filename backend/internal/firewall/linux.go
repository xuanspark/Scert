package firewall

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (m *manager) openLinux(ctx context.Context, port int) (Result, error) {
	ufw, ufwErr := m.runner.LookPath("ufw")
	if ufwErr == nil {
		status, err := m.runner.Run(ctx, ufw, "status")
		if err != nil || status.ExitCode != 0 {
			return Result{}, commandError("检查 UFW 状态", ufw, status, err)
		}
		if ufwIsActive(status.Output) {
			return m.openUFW(ctx, ufw, port, status.Output)
		}
	}

	firewallCMD, firewalldErr := m.runner.LookPath("firewall-cmd")
	if firewalldErr == nil {
		state, err := m.runner.Run(ctx, firewallCMD, "--state")
		if err != nil {
			return Result{}, commandError("检查 firewalld 状态", firewallCMD, state, err)
		}
		if state.ExitCode == 0 && strings.TrimSpace(strings.ToLower(string(state.Output))) == "running" {
			return m.openFirewalld(ctx, firewallCMD, port)
		}
		if state.ExitCode != 0 && state.ExitCode != 252 && !strings.Contains(strings.ToLower(string(state.Output)), "not running") {
			return Result{}, commandError("检查 firewalld 状态", firewallCMD, state, nil)
		}
	}

	return Result{
		Backend: BackendNone, Port: port,
		Message: "未检测到已启用的 UFW 或 firewalld，无需创建主服务端口入站规则",
	}, nil
}

func (m *manager) openUFW(ctx context.Context, executable string, port int, statusOutput []byte) (Result, error) {
	name := ruleName(port)
	if ufwHasRule(statusOutput, port) {
		return Result{
			Backend: backendUFW, RuleName: name, Port: port,
			Message: fmt.Sprintf("UFW 已放行主服务 TCP 端口 %d", port),
		}, nil
	}
	rule := strconv.Itoa(port) + "/tcp"
	added, err := m.runner.Run(ctx, executable, "allow", rule, "comment", name)
	if err != nil || added.ExitCode != 0 {
		return Result{}, commandError("创建 UFW 入站规则", executable, added, err)
	}
	return Result{
		Backend: backendUFW, RuleName: name, Port: port, Created: true,
		Message: fmt.Sprintf("已通过 UFW 放行主服务 TCP 端口 %d", port),
	}, nil
}

func (m *manager) rollbackUFW(ctx context.Context, result Result) error {
	executable, err := m.runner.LookPath("ufw")
	if err != nil {
		return fmt.Errorf("未找到 UFW，无法回滚防火墙规则：%w", err)
	}
	rule := strconv.Itoa(result.Port) + "/tcp"
	removed, err := m.runner.Run(ctx, executable, "--force", "delete", "allow", rule)
	if err != nil || removed.ExitCode != 0 {
		return commandError("回滚 UFW 入站规则", executable, removed, err)
	}
	return nil
}

func (m *manager) openFirewalld(ctx context.Context, executable string, port int) (Result, error) {
	name := ruleName(port)
	rule := strconv.Itoa(port) + "/tcp"
	query, err := m.runner.Run(ctx, executable, "--permanent", "--query-port="+rule)
	if err != nil {
		return Result{}, commandError("检查 firewalld 规则", executable, query, err)
	}
	if query.ExitCode == 0 {
		return Result{
			Backend: backendFirewalld, RuleName: name, Port: port,
			Message: fmt.Sprintf("firewalld 已放行主服务 TCP 端口 %d", port),
		}, nil
	}
	if query.ExitCode != 1 {
		return Result{}, commandError("检查 firewalld 规则", executable, query, nil)
	}
	added, err := m.runner.Run(ctx, executable, "--permanent", "--add-port="+rule)
	if err != nil || added.ExitCode != 0 {
		return Result{}, commandError("创建 firewalld 永久规则", executable, added, err)
	}
	reloaded, err := m.runner.Run(ctx, executable, "--reload")
	if err != nil || reloaded.ExitCode != 0 {
		rollback, rollbackErr := m.runner.Run(ctx, executable, "--permanent", "--remove-port="+rule)
		if rollbackErr == nil && rollback.ExitCode == 0 {
			_, _ = m.runner.Run(ctx, executable, "--reload")
		}
		return Result{}, commandError("重新加载 firewalld", executable, reloaded, err)
	}
	return Result{
		Backend: backendFirewalld, RuleName: name, Port: port, Created: true,
		Message: fmt.Sprintf("已通过 firewalld 放行主服务 TCP 端口 %d", port),
	}, nil
}

func (m *manager) rollbackFirewalld(ctx context.Context, result Result) error {
	executable, err := m.runner.LookPath("firewall-cmd")
	if err != nil {
		return fmt.Errorf("未找到 firewalld，无法回滚防火墙规则：%w", err)
	}
	rule := strconv.Itoa(result.Port) + "/tcp"
	removed, err := m.runner.Run(ctx, executable, "--permanent", "--remove-port="+rule)
	if err != nil || removed.ExitCode != 0 {
		return commandError("回滚 firewalld 永久规则", executable, removed, err)
	}
	reloaded, err := m.runner.Run(ctx, executable, "--reload")
	if err != nil || reloaded.ExitCode != 0 {
		return commandError("重新加载 firewalld", executable, reloaded, err)
	}
	return nil
}

func ufwIsActive(output []byte) bool {
	for _, line := range strings.Split(strings.ToLower(string(output)), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "：", ":"))
		line = strings.Join(strings.Fields(line), " ")
		if line == "status: active" || line == "状态: 激活" {
			return true
		}
	}
	return false
}

func ufwHasRule(output []byte, port int) bool {
	prefix := strconv.Itoa(port) + "/tcp"
	for _, line := range strings.Split(strings.ToLower(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) && strings.Contains(line, "allow") {
			return true
		}
	}
	return false
}
