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
