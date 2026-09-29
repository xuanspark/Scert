package repository

import (
	"context"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type DomainRepository interface {
	Create(ctx context.Context, item domain.Domain) error
	Update(ctx context.Context, item domain.Domain) error
	GetByID(ctx context.Context, id string) (domain.Domain, error)
	List(ctx context.Context) ([]domain.Domain, error)
	Delete(ctx context.Context, id string) error
	UpdateCertificateState(ctx context.Context, id, status string, expiresAt *time.Time) error
}

// ProjectDomainRepository 在项目边界内执行域名资料变更，并将成功审计与业务写入放在同一事务。
type ProjectDomainRepository interface {
	CreateProjectDomain(context.Context, string, domain.Domain) error
	UpdateProjectDomain(context.Context, string, domain.Domain) error
	DeleteProjectDomain(context.Context, string, string) error
}
