package domain

import "time"

// 每次绑定生成新标识；解除后重新绑定也不能让旧页面的写入条件再次成立。
type DNSAccountBinding struct {
	ID           string    `json:"id"`
	DomainID     string    `json:"domain_id"`
	ProjectID    string    `json:"project_id"`
	DNSAccountID string    `json:"dns_account_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}
