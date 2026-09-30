package deployment

import (
	"context"
	"strings"
	"time"
)

// TransactionBackup 描述一次部署事务保存的旧文件。
type TransactionBackup struct {
	Original string `json:"original"`
	Saved    string `json:"saved"`
}

// TransactionRecord 保存远程部署事务的可恢复现场，不包含证书内容或凭据。
type TransactionRecord struct {
	ID              string
	TaskID          string
	AttemptID       string
	AttemptNumber   int
	ResultIndex     int
	ServerID        string
	ServerName      string
	Paths           []string
	TemporaryPaths  []string
	InstalledPaths  []string
	Backups         []TransactionBackup
	LockPaths       []string
	ReloadAttempted bool
	Phase           string
	Status          string
	Message         string
	UpdatedAt       time.Time
}

// TransactionJournal 持久化部署现场，保证进程重启后仍能识别未提交事务。
type TransactionJournal interface {
	CreateDeploymentTransaction(context.Context, TransactionRecord) error
	UpdateDeploymentTransaction(context.Context, TransactionRecord) error
	FinishDeploymentTransaction(context.Context, string, string, string) error
	ListPendingDeploymentTransactions(context.Context) ([]TransactionRecord, error)
}

type transactionContextKey struct{}

// TransactionContext 保存任务与尝试标识，供部署层建立可追溯事务记录。
type TransactionContext struct {
	TaskID        string
	AttemptID     string
	AttemptNumber int
}

// WithTransactionContext 将任务尝试标识绑定到证书部署上下文。
func WithTransactionContext(ctx context.Context, value TransactionContext) context.Context {
	value.TaskID = strings.TrimSpace(value.TaskID)
	value.AttemptID = strings.TrimSpace(value.AttemptID)
	return context.WithValue(ctx, transactionContextKey{}, value)
}

// TransactionContextFromContext 读取任务尝试标识。
func TransactionContextFromContext(ctx context.Context) (TransactionContext, bool) {
	value, ok := ctx.Value(transactionContextKey{}).(TransactionContext)
	if !ok || strings.TrimSpace(value.TaskID) == "" || strings.TrimSpace(value.AttemptID) == "" {
		return TransactionContext{}, false
	}
	return value, true
}
