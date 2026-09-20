//go:build linux

package adminconsole

import "testing"

func TestAuthorizeLinuxProcessRequiresSystemdInvocation(t *testing.T) {
	validID := "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name       string
		euid       int
		pid        int
		parentPID  int
		invocation string
		execPID    string
		want       bool
	}{
		{name: "systemd service", euid: 0, pid: 120, parentPID: 1, invocation: validID, execPID: "120", want: true},
		{name: "systemd without optional exec pid", euid: 0, pid: 120, parentPID: 1, invocation: validID, want: true},
		{name: "non root", euid: 1000, pid: 120, parentPID: 1, invocation: validID, execPID: "120"},
		{name: "direct execution", euid: 0, pid: 120, parentPID: 99, invocation: validID, execPID: "120"},
		{name: "missing invocation", euid: 0, pid: 120, parentPID: 1, execPID: "120"},
		{name: "forged exec pid", euid: 0, pid: 120, parentPID: 1, invocation: validID, execPID: "121"},
		{name: "invalid invocation", euid: 0, pid: 120, parentPID: 1, invocation: "not-an-invocation-id", execPID: "120"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := authorizeLinuxProcess(test.euid, test.pid, test.parentPID, test.invocation, test.execPID); got != test.want {
				t.Fatalf("authorizeLinuxProcess() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAuthorizedSystemdJobOnlyAllowsExplicitStart(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "start running", output: "42 Scert-admin.service start running\n", want: true},
		{name: "start waiting", output: "42 Scert-admin.service start waiting\n", want: true},
		{name: "restart", output: "42 Scert-admin.service restart running\n"},
		{name: "try restart", output: "42 Scert-admin.service try-restart running\n"},
		{name: "reload or restart", output: "42 Scert-admin.service reload-or-restart running\n"},
		{name: "try reload or restart", output: "42 Scert-admin.service try-reload-or-restart running\n"},
		{name: "reload", output: "42 Scert-admin.service reload running\n"},
		{name: "stop", output: "42 Scert-admin.service stop running\n"},
		{name: "other unit", output: "42 smail.service start running\n"},
		{name: "malformed", output: "Scert-admin.service start\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasAuthorizedStartJob(test.output); got != test.want {
				t.Fatalf("hasAuthorizedStartJob() = %v, want %v", got, test.want)
			}
		})
	}
}
