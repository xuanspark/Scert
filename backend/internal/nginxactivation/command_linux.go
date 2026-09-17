package nginxactivation

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"
)

// CommandReloader 的参数来自本地受信策略，证书材料不能指定命令或参数。
type CommandReloader struct {
	Binary string
	Config string
	Prefix string
}

func (r CommandReloader) Check(ctx context.Context) error {
	return r.run(ctx, "-t")
}

func (r CommandReloader) Reload(ctx context.Context) error {
	return r.run(ctx, "-s", "reload")
}

func (r CommandReloader) run(ctx context.Context, operation ...string) error {
	if !filepath.IsAbs(r.Binary) || !filepath.IsAbs(r.Config) || !filepath.IsAbs(r.Prefix) {
		return errors.New("Nginx 命令策略必须使用绝对路径")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	arguments := append([]string{"-p", r.Prefix, "-c", r.Config}, operation...)
	command := exec.CommandContext(ctx, r.Binary, arguments...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	// 不向应用返回可能包含站点配置和文件内容的子进程输出。
	if err := command.Run(); err != nil {
		return errors.Join(errors.New("Nginx 受限命令执行失败"), err, ctx.Err())
	}
	return nil
}
