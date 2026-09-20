//go:build linux

package adminconsole

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type callerIdentity struct {
	pid        int
	started    uint64
	group      int
	terminal   int
	foreground int
}

type invocationTerminal struct {
	file   *os.File
	cancel context.CancelFunc
}

type invocationTerminalKey struct{}

// OpenInvocation 绑定显式启动命令及其终端；systemd 的重启任务进入启动阶段后也会标为 start。
// 这是部署命令约束，受保护通道仍独立校验本机身份和安全凭据。
func OpenInvocation(parent context.Context) (context.Context, func(), error) {
	if err := AuthorizeCurrentProcess(); err != nil {
		return nil, nil, err
	}
	caller, err := findStartCaller()
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/fd/0", caller.pid), unix.O_RDWR|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, ErrUnauthorizedPeer
	}
	file := os.NewFile(uintptr(fd), "Scert-admin-terminal")
	var info unix.Stat_t
	err = unix.Fstat(fd, &info)
	device := uint32(caller.terminal)
	expectedDevice := unix.Mkdev((device>>8)&0xfff, (device&0xff)|((device>>12)&0xfff00))
	// 服务不属于调用终端的会话，不能使用 TIOCGPGRP；前台关系由 /proc 校验。
	if err != nil || info.Rdev != expectedDevice || !isTerminal(fd) {
		file.Close()
		return nil, nil, ErrUnauthorizedPeer
	}
	ctx, cancel := context.WithCancel(parent)
	terminal := &invocationTerminal{file: file, cancel: cancel}
	ctx = context.WithValue(ctx, invocationTerminalKey{}, terminal)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := readCallerIdentity(caller.pid)
				if err != nil || current != caller {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
		file.Close()
	}, nil
}

func findStartCaller() (callerIdentity, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return callerIdentity{}, ErrUnauthorizedPeer
	}
	self, err := readCallerIdentity(os.Getpid())
	if err != nil {
		return callerIdentity{}, ErrUnauthorizedPeer
	}
	var callers []callerIdentity
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		executable, err := os.Readlink(filepath.Join(base, "exe"))
		if err != nil || (executable != "/usr/bin/systemctl" && executable != "/bin/systemctl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(base, "cmdline"))
		if err != nil {
			continue
		}
		targets, allowed := classifyAdminCommand(strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"))
		if !targets {
			continue
		}
		// 同时存在重启、停止或多个启动请求时拒绝，不能借用另一个 start 进程授权。
		if !allowed {
			return callerIdentity{}, ErrUnauthorizedPeer
		}
		status, err := os.ReadFile(filepath.Join(base, "status"))
		if err != nil || !rootProcessStatus(string(status)) {
			return callerIdentity{}, ErrUnauthorizedPeer
		}
		caller, err := readCallerIdentity(pid)
		if err != nil || caller.started > self.started || caller.terminal == 0 || caller.group != caller.foreground {
			return callerIdentity{}, ErrUnauthorizedPeer
		}
		callers = append(callers, caller)
	}
	if len(callers) != 1 {
		return callerIdentity{}, ErrUnauthorizedPeer
	}
	return callers[0], nil
}

func classifyAdminCommand(args []string) (targets, allowed bool) {
	if len(args) < 3 {
		return false, false
	}
	mutating := false
	for _, arg := range args[1:] {
		switch arg {
		case "start", "stop", "restart", "try-restart", "reload", "reload-or-restart", "try-reload-or-restart", "force-reload", "condrestart":
			mutating = true
		}
		for _, unit := range []string{"Scert-admin", "Scert-admin.service"} {
			if match, _ := filepath.Match(arg, unit); match {
				targets = true
			}
		}
	}
	if !mutating || !targets {
		return false, false
	}
	var positional []string
	for _, arg := range args[1:] {
		switch arg {
		case "--system", "--no-pager", "--no-legend", "--plain", "--quiet", "-q", "--wait":
			continue
		default:
			positional = append(positional, arg)
		}
	}
	return true, len(positional) == 2 && positional[0] == "start" && (positional[1] == "Scert-admin" || positional[1] == "Scert-admin.service")
}

func rootProcessStatus(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == "Uid:" {
			return fields[1] == "0" && fields[2] == "0" && fields[3] == "0" && fields[4] == "0"
		}
	}
	return false
}

func readCallerIdentity(pid int) (callerIdentity, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return callerIdentity{}, err
	}
	return parseCallerIdentity(pid, string(data))
}

func parseCallerIdentity(pid int, data string) (callerIdentity, error) {
	end := strings.LastIndex(data, ") ")
	if end < 0 {
		return callerIdentity{}, ErrUnauthorizedPeer
	}
	fields := strings.Fields(data[end+2:])
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return callerIdentity{}, ErrUnauthorizedPeer
	}
	values := make([]int, 3)
	for i, index := range []int{2, 4, 5} {
		value, err := strconv.Atoi(fields[index])
		if err != nil {
			return callerIdentity{}, ErrUnauthorizedPeer
		}
		values[i] = value
	}
	started, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return callerIdentity{}, ErrUnauthorizedPeer
	}
	return callerIdentity{pid: pid, started: started, group: values[0], terminal: values[1], foreground: values[2]}, nil
}
