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
	Exists(ctx context.Context, path string) (bool, error)
	Upload(ctx context.Context, path string, data []byte, mode uint32) error
	Rename(ctx context.Context, source, target string) error
	Remove(ctx context.Context, path string) error
	Run(ctx context.Context, command []string) (CommandResult, error)
	Close() error
}

type ClientFactory interface {
	Connect(ctx context.Context, server Server) (Client, error)
}
