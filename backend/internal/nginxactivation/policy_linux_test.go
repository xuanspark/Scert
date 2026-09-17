package nginxactivation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func newPolicyFixture(t *testing.T) (Policy, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid()) + 1
	policy := Policy{
		Version: 1, AllowedUID: &uid,
		SocketPath: filepath.Join(directory, "a.sock"), Directory: directory,
		Hostname: testHostname, ProxyAddress: "127.0.0.1:443", UpstreamAddress: "127.0.0.1:9004",
		NginxBinary: filepath.Join(directory, "nginx"), NginxConfig: filepath.Join(directory, "nginx.conf"),
		NginxPrefix: directory,
	}
	writeTestFile(t, policy.NginxBinary, []byte("test executable"))
	if err := os.Chmod(policy.NginxBinary, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, policy.NginxConfig, []byte("events {}\n"))
	return policy, filepath.Join(directory, "policy.json")
}

func writePolicy(t *testing.T, path string, policy Policy) {
	t.Helper()
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, data)
}

func TestPolicyValidation(t *testing.T) {
	for _, scenario := range []string{
		"valid", "version", "missing-uid", "root-uid", "socket-relative", "socket-long", "socket-parent",
		"directory-mode", "hostname", "proxy-public", "upstream-public", "same-address", "binary-mode",
		"binary-no-exec", "binary-setuid", "config-mode", "prefix-relative", "prefix-mode",
	} {
		t.Run(scenario, func(t *testing.T) {
			policy, path := newPolicyFixture(t)
			chmod := func(path string, mode os.FileMode) {
				t.Helper()
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "version":
				policy.Version = 2
			case "missing-uid":
				policy.AllowedUID = nil
			case "root-uid":
				*policy.AllowedUID = 0
			case "socket-relative":
				policy.SocketPath = "a.sock"
			case "socket-long":
				policy.SocketPath = "/" + strings.Repeat("a", 108)
			case "socket-parent":
				parent := filepath.Join(policy.Directory, "socket")
				if err := os.Mkdir(parent, 0o700); err != nil {
					t.Fatal(err)
				}
				chmod(parent, 0o777)
				policy.SocketPath = filepath.Join(parent, "a.sock")
			case "directory-mode":
				chmod(policy.Directory, 0o755)
			case "hostname":
				policy.Hostname = "*.example.com"
			case "proxy-public":
				policy.ProxyAddress = "192.0.2.1:443"
			case "upstream-public":
				policy.UpstreamAddress = "0.0.0.0:9004"
			case "same-address":
				policy.UpstreamAddress = policy.ProxyAddress
			case "binary-mode":
				chmod(policy.NginxBinary, 0o777)
			case "binary-no-exec":
				chmod(policy.NginxBinary, 0o600)
			case "binary-setuid":
				chmod(policy.NginxBinary, 0o700|os.ModeSetuid)
			case "config-mode":
				chmod(policy.NginxConfig, 0o666)
			case "prefix-relative":
				policy.NginxPrefix = "."
			case "prefix-mode":
				policy.NginxPrefix = filepath.Join(policy.Directory, "prefix")
				if err := os.Mkdir(policy.NginxPrefix, 0o700); err != nil {
					t.Fatal(err)
				}
				chmod(policy.NginxPrefix, 0o777)
			}
			writePolicy(t, path, policy)
			got, err := loadPolicy(path, uint32(os.Geteuid()))
			if scenario == "valid" {
				if err != nil || got.Hostname != policy.Hostname || got.Rotation().Directory != policy.Directory {
					t.Fatal("合法策略未正确加载", err)
				}
			} else if err == nil {
				t.Fatal("不可信策略被接受")
			}
		})
	}
}

func TestPolicyFileSafety(t *testing.T) {
	for _, scenario := range []string{"symlink", "hardlink", "fifo", "writable", "owner", "oversize", "unknown", "trailing", "null"} {
		t.Run(scenario, func(t *testing.T) {
			policy, path := newPolicyFixture(t)
			writePolicy(t, path, policy)
			owner := uint32(os.Geteuid())
			switch scenario {
			case "symlink":
				link := path + ".link"
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "hardlink":
				if err := os.Link(path, path+".link"); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				path += ".fifo"
				if err := unix.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			case "owner":
				owner++
			case "oversize":
				writeTestFile(t, path, []byte(strings.Repeat(" ", (16<<10)+1)))
			case "unknown", "trailing":
				data, err := json.Marshal(policy)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "unknown" {
					data = append(data[:len(data)-1], []byte(`,"command":"id"}`)...)
				} else {
					data = append(data, []byte(`{}`)...)
				}
				writeTestFile(t, path, data)
			case "null":
				writeTestFile(t, path, []byte("null"))
			}
			if _, err := loadPolicy(path, owner); err == nil {
				t.Fatal("不可信策略文件被接受")
			}
		})
	}
}
