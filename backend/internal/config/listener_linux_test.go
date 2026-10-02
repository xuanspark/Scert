package config

import "testing"

func TestLinuxServerDefaultsToLoopback(t *testing.T) {
	cfg := Default(t.TempDir())
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatal("Linux 默认监听地址不是回环地址")
	}
	cfg.Server.Host = ""
	cfg.Normalize()
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatal("空监听地址未回退到回环地址")
	}
}

func TestLinuxServerListenerRejectsNonLoopback(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::", "192.0.2.1", "10.0.0.1", "2001:db8::1", "localhost", "127.0.0.1.example.test", " 127.0.0.1", "127.0.0.1:8443"} {
		t.Run(host, func(t *testing.T) {
			cfg := Default(t.TempDir())
			cfg.Server.Host = host
			if err := cfg.ValidateServerListener(); err == nil {
				t.Fatal("非回环或模糊监听配置未被拒绝")
			}
		})
	}
	for _, host := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		t.Run(host, func(t *testing.T) {
			cfg := Default(t.TempDir())
			cfg.Server.Host = host
			if err := cfg.ValidateServerListener(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
