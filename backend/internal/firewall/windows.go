package firewall

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	windowsStatusScript = `$ErrorActionPreference='Stop'; if ((Get-NetFirewallProfile | Where-Object { $_.Enabled }).Count -gt 0) { Write-Output 'enabled' } else { Write-Output 'disabled' }`
	windowsQueryScript  = `$ErrorActionPreference='Stop'; $rule=Get-NetFirewallRule -DisplayName $args[0] -ErrorAction SilentlyContinue; if ($null -eq $rule) { exit 3 }; exit 0`
	windowsAddScript    = `$ErrorActionPreference='Stop'; New-NetFirewallRule -DisplayName $args[0] -Direction Inbound -Action Allow -Enabled True -Profile Any -Protocol TCP -LocalPort ([int]$args[1]) | Out-Null`
	windowsRemoveScript = `$ErrorActionPreference='Stop'; Remove-NetFirewallRule -DisplayName $args[0]`
)

func (m *manager) openWindows(ctx context.Context, port int) (Result, error) {
	powershell, err := m.runner.LookPath("powershell.exe")
	if err != nil {
		return Result{}, fmt.Errorf("未找到 Windows PowerShell，无法配置本机防火墙：%w", err)
	}
	status, err := m.runner.Run(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", windowsStatusScript)
	if err != nil || status.ExitCode != 0 {
		return Result{}, commandError("检查 Windows 防火墙状态", powershell, status, err)
	}
	if !strings.Contains(strings.ToLower(string(status.Output)), "enabled") {
		return Result{
			Backend: BackendNone, Port: port,
			Message: "Windows 防火墙当前未启用，无需创建主服务端口入站规则",
		}, nil
	}

	name := ruleName(port)
	query, err := m.runner.Run(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", windowsQueryScript, name)
	if err != nil {
		return Result{}, commandError("检查 Windows 防火墙规则", powershell, query, err)
	}
	if query.ExitCode == 0 {
		return Result{
			Backend: backendWindows, RuleName: name, Port: port,
			Message: fmt.Sprintf("Windows 防火墙规则已存在：%s", name),
		}, nil
	}
	if query.ExitCode != 3 {
		return Result{}, commandError("检查 Windows 防火墙规则", powershell, query, nil)
	}

	added, err := m.runner.Run(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", windowsAddScript, name, strconv.Itoa(port))
	if err != nil || added.ExitCode != 0 {
		return Result{}, commandError("创建 Windows 防火墙入站规则", powershell, added, err)
	}
	return Result{
		Backend: backendWindows, RuleName: name, Port: port, Created: true,
		Message: fmt.Sprintf("已创建 Windows 防火墙规则：%s", name),
	}, nil
}

func (m *manager) rollbackWindows(ctx context.Context, result Result) error {
	powershell, err := m.runner.LookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("未找到 Windows PowerShell，无法回滚防火墙规则：%w", err)
	}
	removed, err := m.runner.Run(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", windowsRemoveScript, result.RuleName)
	if err != nil || removed.ExitCode != 0 {
		return commandError("回滚 Windows 防火墙规则", powershell, removed, err)
	}
	return nil
}
