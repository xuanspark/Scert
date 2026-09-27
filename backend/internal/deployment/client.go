package deployment

import "context"

type CommandResult struct {
	Output   []byte
	ExitCode int
}

type Client interface {
	Lock(ctx context.Context, path string) error
	Unlock(ctx context.Context, path string) error
	MkdirAll(ctx context.Context, path string) error
	List(ctx context.Context, path string) ([]string, error)
	Exists(ctx context.Context, path string) (bool, error)
	Upload(ctx context.Context, path string, data []byte, mode uint32) error
	Rename(ctx context.Context, source, target string) error
	Remove(ctx context.Context, path string) error
	Run(ctx context.Context, command []string) (CommandResult, error)
	InstallAuthorizedKey(ctx context.Context, publicKey string) error
	Close() error
}

type ClientFactory interface {
	Connect(ctx context.Context, server Server) (Client, error)
}

// ShellClientFactory 为只需要 SSH Shell 的操作提供连接入口，避免无关的 SFTP 子系统阻塞密钥部署。
type ShellClientFactory interface {
	ConnectShell(ctx context.Context, server Server) (Client, error)
}
