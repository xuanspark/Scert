//go:build linux

package adminconsole

import (
	"context"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/localreset"
	"golang.org/x/sys/unix"
)

func openTerminal(ctx context.Context) (context.Context, terminal, func(), error) {
	child, closeTerminal, err := OpenInvocation(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	screen, ok := child.Value(invocationTerminalKey{}).(*invocationTerminal)
	if !ok {
		closeTerminal()
		return nil, nil, nil, ErrUnauthorizedPeer
	}
	return child, screen, closeTerminal, nil
}

func (t *invocationTerminal) Read(ctx context.Context, prompt string, hidden bool) (string, error) {
	return t.read(ctx, prompt, hidden)
}

func (t *invocationTerminal) Show(message string) error { return showLine(t.file, message) }

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

func dialControl(ctx context.Context) (net.Conn, error) {
	serviceUser, err := user.Lookup("Scert")
	if err != nil {
		return nil, localreset.ErrUnavailable
	}
	uid, err := strconv.ParseUint(serviceUser.Uid, 10, 32)
	if err != nil {
		return nil, localreset.ErrUnavailable
	}
	return localreset.Dial(ctx, "", uint32(uid))
}

// AuthorizeCurrentProcess 要求 root 系统服务和显式启动任务，不能通过直接执行或伪造环境放行。
func AuthorizeCurrentProcess() error {
	if !authorizeLinuxProcess(os.Geteuid(), os.Getpid(), os.Getppid(), os.Getenv("INVOCATION_ID"), os.Getenv("SYSTEMD_EXEC_PID")) {
		return ErrUnauthorizedPeer
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "list-jobs", "--no-legend", "--plain", "--no-pager").Output()
	if err != nil || !hasAuthorizedStartJob(string(output)) {
		return ErrUnauthorizedPeer
	}
	return nil
}

func hasAuthorizedStartJob(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[1] == "Scert-admin.service" {
			return fields[2] == "start" && (fields[3] == "waiting" || fields[3] == "running")
		}
	}
	return false
}

func authorizeLinuxProcess(euid, pid, parentPID int, invocationID, systemdExecPID string) bool {
	if euid != 0 || parentPID != 1 || !validInvocationID(invocationID) {
		return false
	}
	if systemdExecPID == "" {
		return true
	}
	execPID, err := strconv.Atoi(systemdExecPID)
	return err == nil && execPID == pid
}

func validInvocationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range strings.ToLower(value) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
