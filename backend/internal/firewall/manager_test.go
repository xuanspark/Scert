package firewall

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type runnerCall struct {
	name string
	args []string
}

type runnerResponse struct {
	result CommandResult
	err    error
}

type fakeRunner struct {
	paths     map[string]string
	responses []runnerResponse
	calls     []runnerCall
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	path, ok := f.paths[name]
	if !ok {
		return "", errors.New("not found")
	}
	return path, nil
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	f.calls = append(f.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	if len(f.responses) == 0 {
		return CommandResult{}, errors.New("unexpected command")
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response.result, response.err
}

func TestOpenTCPRejectsInvalidPortBeforeRunningCommand(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManagerForOS("windows", runner)
	if _, err := manager.OpenTCP(context.Background(), 0); err == nil {
		t.Fatal("无效端口应被拒绝")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("无效端口不应执行系统命令：%+v", runner.calls)
	}
}

func TestWindowsCreatesIdempotentRuleAndRollsBackOwnChange(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]string{"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		responses: []runnerResponse{
			{result: CommandResult{Output: []byte("enabled\n")}},
			{result: CommandResult{ExitCode: 3}},
			{result: CommandResult{}},
			{result: CommandResult{}},
		},
	}
	manager := NewManagerForOS("windows", runner)
	result, err := manager.OpenTCP(context.Background(), 8443)
	if err != nil {
		t.Fatalf("创建 Windows 防火墙规则失败：%v", err)
	}
	if !result.Created || result.Backend != backendWindows || result.RuleName != "Scert Web TCP 8443" {
		t.Fatalf("Windows 防火墙结果不正确：%+v", result)
	}
	addArgs := runner.calls[2].args
	if got := addArgs[len(addArgs)-2:]; !reflect.DeepEqual(got, []string{"Scert Web TCP 8443", "8443"}) {
		t.Fatalf("端口和规则名称必须通过独立参数传递：%v", addArgs)
	}
	if err := manager.Rollback(context.Background(), result); err != nil {
		t.Fatalf("回滚 Windows 防火墙规则失败：%v", err)
	}
	removeArgs := runner.calls[3].args
	if removeArgs[len(removeArgs)-1] != result.RuleName {
		t.Fatalf("回滚规则名称不正确：%v", removeArgs)
	}
}

func TestWindowsSkipsRuleWhenFirewallIsDisabled(t *testing.T) {
	runner := &fakeRunner{
		paths:     map[string]string{"powershell.exe": "powershell.exe"},
		responses: []runnerResponse{{result: CommandResult{Output: []byte("disabled\n")}}},
	}
	result, err := NewManagerForOS("windows", runner).OpenTCP(context.Background(), 443)
	if err != nil {
		t.Fatalf("检查 Windows 防火墙失败：%v", err)
	}
	if result.Created || result.Backend != BackendNone || len(runner.calls) != 1 {
		t.Fatalf("Windows 防火墙未启用时不应创建规则：%+v calls=%+v", result, runner.calls)
	}
}

func TestUFWCreatesRuleAndRollbackUsesExactPortArgument(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]string{"ufw": "/usr/sbin/ufw"},
		responses: []runnerResponse{
			{result: CommandResult{Output: []byte("Status: active\n")}},
			{result: CommandResult{}},
			{result: CommandResult{}},
		},
	}
	manager := NewManagerForOS("linux", runner)
	result, err := manager.OpenTCP(context.Background(), 8443)
	if err != nil {
		t.Fatalf("创建 UFW 规则失败：%v", err)
	}
	if !result.Created || result.Backend != backendUFW {
		t.Fatalf("UFW 结果不正确：%+v", result)
	}
	if got := runner.calls[1].args; !reflect.DeepEqual(got, []string{"allow", "8443/tcp", "comment", "Scert Web TCP 8443"}) {
		t.Fatalf("UFW 创建参数不正确：%v", got)
	}
	if err := manager.Rollback(context.Background(), result); err != nil {
		t.Fatalf("回滚 UFW 规则失败：%v", err)
	}
	if got := runner.calls[2].args; !reflect.DeepEqual(got, []string{"--force", "delete", "allow", "8443/tcp"}) {
		t.Fatalf("UFW 回滚参数不正确：%v", got)
	}
}

func TestUFWKeepsExistingRule(t *testing.T) {
	runner := &fakeRunner{
		paths:     map[string]string{"ufw": "/usr/sbin/ufw"},
		responses: []runnerResponse{{result: CommandResult{Output: []byte("Status: active\n8443/tcp ALLOW Anywhere\n")}}},
	}
	result, err := NewManagerForOS("linux", runner).OpenTCP(context.Background(), 8443)
	if err != nil {
		t.Fatalf("检查 UFW 规则失败：%v", err)
	}
	if result.Created || result.Backend != backendUFW || len(runner.calls) != 1 {
		t.Fatalf("已有 UFW 规则不应重复创建：%+v calls=%+v", result, runner.calls)
	}
}

func TestFirewalldCreatesPermanentRuleAndReloads(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]string{"firewall-cmd": "/usr/bin/firewall-cmd"},
		responses: []runnerResponse{
			{result: CommandResult{Output: []byte("running\n")}},
			{result: CommandResult{ExitCode: 1}},
			{result: CommandResult{}},
			{result: CommandResult{}},
			{result: CommandResult{}},
			{result: CommandResult{}},
		},
	}
	manager := NewManagerForOS("linux", runner)
	result, err := manager.OpenTCP(context.Background(), 9443)
	if err != nil {
		t.Fatalf("创建 firewalld 规则失败：%v", err)
	}
	if !result.Created || result.Backend != backendFirewalld {
		t.Fatalf("firewalld 结果不正确：%+v", result)
	}
	if got := runner.calls[2].args; !reflect.DeepEqual(got, []string{"--permanent", "--add-port=9443/tcp"}) {
		t.Fatalf("firewalld 创建参数不正确：%v", got)
	}
	if err := manager.Rollback(context.Background(), result); err != nil {
		t.Fatalf("回滚 firewalld 规则失败：%v", err)
	}
	if got := runner.calls[4].args; !reflect.DeepEqual(got, []string{"--permanent", "--remove-port=9443/tcp"}) {
		t.Fatalf("firewalld 回滚参数不正确：%v", got)
	}
}

func TestFirewalldReloadFailureRemovesNewPermanentRule(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]string{"firewall-cmd": "/usr/bin/firewall-cmd"},
		responses: []runnerResponse{
			{result: CommandResult{Output: []byte("running\n")}},
			{result: CommandResult{ExitCode: 1}},
			{result: CommandResult{}},
			{result: CommandResult{Output: []byte("reload failed"), ExitCode: 2}},
			{result: CommandResult{}},
			{result: CommandResult{}},
		},
	}
	_, err := NewManagerForOS("linux", runner).OpenTCP(context.Background(), 9443)
	if err == nil {
		t.Fatal("firewalld 重新加载失败时初始化应失败")
	}
	if got := runner.calls[4].args; !reflect.DeepEqual(got, []string{"--permanent", "--remove-port=9443/tcp"}) {
		t.Fatalf("重新加载失败时应移除本次新增规则：%v", got)
	}
}

func TestFirewalldStatePermissionFailureIsNotReportedAsDisabled(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]string{"firewall-cmd": "/usr/bin/firewall-cmd"},
		responses: []runnerResponse{{result: CommandResult{
			Output: []byte("Authorization failed"), ExitCode: 2,
		}}},
	}
	result, err := NewManagerForOS("linux", runner).OpenTCP(context.Background(), 8443)
	if err == nil {
		t.Fatalf("firewalld 权限错误不得被报告为未启用：%+v", result)
	}
}
