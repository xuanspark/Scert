package firewall

import "context"

const BackendNone = "none"

type Result struct {
	Backend  string `json:"backend"`
	RuleName string `json:"rule_name,omitempty"`
	Port     int    `json:"port"`
	Created  bool   `json:"created"`
	Message  string `json:"message"`
}

type Manager interface {
	OpenTCP(ctx context.Context, port int) (Result, error)
	Rollback(ctx context.Context, result Result) error
}

type CommandResult struct {
	Output   []byte
	ExitCode int
}

type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}
