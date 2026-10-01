package sshkey

import (
	"context"
	"errors"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

var ErrDisabled = errors.New("SSH 密钥登录功能已永久停用，仅支持 SSH 密码认证")

type Store interface {
	Put(ctx context.Context, reference string, value map[string]string) error
	Get(ctx context.Context, reference string) (map[string]string, error)
	List(ctx context.Context, prefix string) ([]secretstore.Metadata, error)
	Delete(ctx context.Context, reference string) error
}

// Metadata 是可安全返回给 API、CLI 和前端的 SSH 私钥元数据。
type Metadata struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Reference   string    `json:"reference"`
	Algorithm   string    `json:"algorithm"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
	ProjectID   string    `json:"project_id,omitempty"`
}

type Service struct {
}

func NewService(_ ...Store) *Service {
	return &Service{}
}

func (s *Service) Create(context.Context, string, string, string) (Metadata, error) {
	return Metadata{}, ErrDisabled
}

func (s *Service) CreateForProject(context.Context, string, string, string, string) (Metadata, error) {
	return Metadata{}, ErrDisabled
}

func (s *Service) CreateGenerated(context.Context, string) (Metadata, string, error) {
	return Metadata{}, "", ErrDisabled
}

func (s *Service) CreateGeneratedForProject(context.Context, string, string) (Metadata, string, error) {
	return Metadata{}, "", ErrDisabled
}
func (s *Service) AuthorizedPublicKey(context.Context, string) (string, error) {
	return "", ErrDisabled
}
func (s *Service) List(context.Context) ([]Metadata, error) {
	return nil, ErrDisabled
}

func (s *Service) ListForProject(context.Context, string) ([]Metadata, error) {
	return nil, ErrDisabled
}
func (s *Service) ValidateReference(context.Context, string) (Metadata, error) {
	return Metadata{}, ErrDisabled
}

func (s *Service) ValidateReferenceForProject(context.Context, string, string) (Metadata, error) {
	return Metadata{}, ErrDisabled
}

func (s *Service) Delete(context.Context, string) error {
	return ErrDisabled
}

func (s *Service) Reference(string) (string, error) {
	return "", ErrDisabled
}
