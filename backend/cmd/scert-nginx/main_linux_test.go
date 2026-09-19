package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExecuteRejectsInvalidInitializationArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--initialize"}, {"--initialize", "--certificate", "/no-certificate"},
		{"--initialize", "--private-key", "/no-key"}, {"--certificate", "/no-certificate"},
		{"--private-key", "/no-key"}, {"--policy", ""}, {"unexpected"},
	} {
		if err := execute(context.Background(), args); err == nil {
			t.Fatalf("无效初始化参数被接受：%v", args)
		}
	}
}

func TestInheritedListener(t *testing.T) {
	if mode := os.Getenv("SCERT_LISTENER_TEST_CHILD"); mode != "" {
		os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
		os.Setenv("LISTEN_FDS", "1")
		switch mode {
		case "pid":
			os.Setenv("LISTEN_PID", "0")
		case "fds":
			os.Setenv("LISTEN_FDS", "2")
		}
		listener, err := inheritedListener(os.Getenv("SCERT_LISTENER_TEST_PATH"))
		if mode == "valid" {
			if err != nil {
				t.Fatal(err)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(os.Getenv("SCERT_LISTENER_TEST_PATH")); err != nil {
				t.Fatal("继承监听器关闭不应删除 systemd socket", err)
			}
		} else if err == nil {
			listener.Close()
			t.Fatal("无效继承监听器被接受")
		}
		fmt.Println("INHERITED_LISTENER_VERIFIED")
		return
	}
	for _, mode := range []string{"valid", "pid", "fds", "path", "tcp"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "a.sock")
			var listener net.Listener
			var file *os.File
			var err error
			if mode == "tcp" {
				tcp, listenErr := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if listenErr != nil {
					t.Fatal(listenErr)
				}
				listener = tcp
				file, err = tcp.File()
			} else {
				unix, listenErr := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if listenErr != nil {
					t.Fatal(listenErr)
				}
				unix.SetUnlinkOnClose(false)
				listener = unix
				file, err = unix.File()
			}
			defer listener.Close()
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if mode == "path" {
				path += ".wrong"
			}
			command := exec.Command(os.Args[0], "-test.run=^TestInheritedListener$")
			command.Env = append(os.Environ(), "SCERT_LISTENER_TEST_CHILD="+mode, "SCERT_LISTENER_TEST_PATH="+path)
			command.ExtraFiles = []*os.File{file}
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "INHERITED_LISTENER_VERIFIED") {
				t.Fatalf("继承监听器子进程失败：%s, %v", output, err)
			}
		})
	}
}
