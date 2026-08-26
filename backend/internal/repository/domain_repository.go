package repository

import (
	"context"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

type DomainRepository interface {
	Create(ctx context.Context, item domain.Domain) error
	GetByID(ctx context.Context, id string) (domain.Domain, error)
	List(ctx context.Context) ([]domain.Domain, error)
	Delete(ctx context.Context, id string) error
}
