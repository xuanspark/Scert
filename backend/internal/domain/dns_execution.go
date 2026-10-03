package domain

// DNSExecutionIdentity 仅由服务端任务执行器创建，不从 HTTP 请求解码。
type DNSExecutionIdentity struct {
	TaskID        string
	AttemptID     string
	AttemptNumber int
	Scheduled     bool
}

// DNSExecutionLease 是秘密使用能力，不允许出现在 API 响应或日志中。
type DNSExecutionLease struct {
	ID            string               `json:"-"`
	DomainID      string               `json:"-"`
	BindingID     string               `json:"-"`
	ProjectID     string               `json:"-"`
	AccountID     string               `json:"-"`
	Revision      int64                `json:"-"`
	Provider      string               `json:"-"`
	CredentialRef string               `json:"-"`
	Identity      DNSExecutionIdentity `json:"-"`
}
