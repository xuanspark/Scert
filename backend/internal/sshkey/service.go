package sshkey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

const (
	ReferencePrefix    = "secret://ssh/"
	secretKind         = "ssh_private_key"
	MaxPrivateKeyBytes = 1024 * 1024
	maxNameLength      = 128
	maxPassphraseBytes = 4096
)

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
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Create(ctx context.Context, name, privateKey, passphrase string) (Metadata, error) {
	if s == nil || s.store == nil {
		return Metadata{}, errors.New("SSH 私钥存储未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Metadata{}, errors.New("SSH 私钥名称不能为空")
	}
	if len([]rune(name)) > maxNameLength {
		return Metadata{}, fmt.Errorf("SSH 私钥名称不能超过 %d 个字符", maxNameLength)
	}
	privateKey = strings.TrimSpace(privateKey)
	if privateKey == "" {
		return Metadata{}, errors.New("SSH 私钥内容不能为空")
	}
	if len(privateKey) > MaxPrivateKeyBytes {
		return Metadata{}, fmt.Errorf("SSH 私钥内容不能超过 %d 字节", MaxPrivateKeyBytes)
	}
	if len(passphrase) > maxPassphraseBytes {
		return Metadata{}, fmt.Errorf("SSH 私钥口令不能超过 %d 字节", maxPassphraseBytes)
	}
	signer, err := parsePrivateKey(privateKey, passphrase)
	if err != nil {
		return Metadata{}, err
	}
	id := domain.NewID()
	reference := ReferencePrefix + id
	payload := map[string]string{
		"kind":        secretKind,
		"name":        name,
		"private_key": privateKey + "\n",
		"passphrase":  passphrase,
		"algorithm":   signer.PublicKey().Type(),
		"fingerprint": ssh.FingerprintSHA256(signer.PublicKey()),
	}
	if err := s.store.Put(ctx, reference, payload); err != nil {
		return Metadata{}, fmt.Errorf("保存 SSH 私钥失败：%w", err)
	}
	return Metadata{
		ID: id, Name: name, Reference: reference, Algorithm: payload["algorithm"],
		Fingerprint: payload["fingerprint"], UpdatedAt: time.Now().UTC(),
	}, nil
}

func (s *Service) CreateGenerated(ctx context.Context, name string) (Metadata, string, error) {
	if s == nil || s.store == nil {
		return Metadata{}, "", errors.New("SSH 私钥存储未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Metadata{}, "", errors.New("SSH 私钥名称不能为空")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Metadata{}, "", fmt.Errorf("生成 SSH 密钥失败：%w", err)
	}
	privatePEM, err := ssh.MarshalPrivateKey(privateKey, "scert-deploy")
	if err != nil {
		return Metadata{}, "", fmt.Errorf("编码 SSH 私钥失败：%w", err)
	}
	metadata, err := s.Create(ctx, name, string(pem.EncodeToMemory(privatePEM)), "")
	if err != nil {
		return Metadata{}, "", err
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return Metadata{}, "", fmt.Errorf("编码 SSH 公钥失败：%w", err)
	}
	return metadata, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey))), nil
}
func (s *Service) AuthorizedPublicKey(ctx context.Context, reference string) (string, error) {
    if s == nil || s.store == nil {
        return "", errors.New("SSH 私钥存储未初始化")
    }
    metadata, err := s.metadata(ctx, strings.TrimSpace(reference), time.Time{}, true)
    if err != nil {
        return "", err
    }
    payload, err := s.store.Get(ctx, metadata.Reference)
    if err != nil {
        return "", fmt.Errorf("读取 SSH 私钥失败：%w", err)
    }
    signer, err := parsePrivateKey(payload["private_key"], payload["passphrase"])
    if err != nil {
        return "", err
    }
    publicKey, err := ssh.NewPublicKey(signer.PublicKey())
    if err != nil {
        return "", fmt.Errorf("编码 SSH 公钥失败：%w", err)
    }
    return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))), nil
}
func (s *Service) List(ctx context.Context) ([]Metadata, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("SSH 私钥存储未初始化")
	}
	items, err := s.store.List(ctx, ReferencePrefix)
	if err != nil {
		return nil, fmt.Errorf("列出 SSH 私钥失败：%w", err)
	}
	result := make([]Metadata, 0, len(items))
	for _, item := range items {
		referenceID := strings.TrimPrefix(item.Reference, ReferencePrefix)
		if strings.Contains(referenceID, "/") {
			continue
		}
		metadata, err := s.metadata(ctx, item.Reference, item.UpdatedAt, false)
		if err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	return result, nil
}

func (s *Service) ValidateReference(ctx context.Context, reference string) (Metadata, error) {
	if s == nil || s.store == nil {
		return Metadata{}, errors.New("SSH 私钥存储未初始化")
	}
	return s.metadata(ctx, strings.TrimSpace(reference), time.Time{}, true)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if s == nil || s.store == nil {
		return errors.New("SSH 私钥存储未初始化")
	}
	reference, err := referenceFromID(id)
	if err != nil {
		return err
	}
	if _, err := s.metadata(ctx, reference, time.Time{}, false); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, reference); err != nil {
		return fmt.Errorf("删除 SSH 私钥失败：%w", err)
	}
	return nil
}

func (s *Service) Reference(id string) (string, error) {
	return referenceFromID(id)
}

func (s *Service) metadata(ctx context.Context, reference string, updatedAt time.Time, validateKey bool) (Metadata, error) {
	if !strings.HasPrefix(reference, ReferencePrefix) {
		return Metadata{}, errors.New("SSH 私钥引用格式无效")
	}
	id := strings.TrimPrefix(reference, ReferencePrefix)
	if _, err := referenceFromID(id); err != nil {
		return Metadata{}, err
	}
	payload, err := s.store.Get(ctx, reference)
	if err != nil {
		return Metadata{}, fmt.Errorf("读取 SSH 私钥失败：%w", err)
	}
	if payload["kind"] != secretKind || strings.TrimSpace(payload["private_key"]) == "" {
		return Metadata{}, errors.New("敏感信息引用不是有效的 SSH 私钥")
	}
	algorithm := payload["algorithm"]
	fingerprint := payload["fingerprint"]
	if validateKey || algorithm == "" || fingerprint == "" {
		signer, parseErr := parsePrivateKey(payload["private_key"], payload["passphrase"])
		if parseErr != nil {
			return Metadata{}, parseErr
		}
		algorithm = signer.PublicKey().Type()
		fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
	}
	return Metadata{
		ID: id, Name: payload["name"], Reference: reference,
		Algorithm: algorithm, Fingerprint: fingerprint, UpdatedAt: updatedAt,
	}, nil
}

func parsePrivateKey(privateKey, passphrase string) (ssh.Signer, error) {
	var (
		signer ssh.Signer
		err    error
	)
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(privateKey))
	}
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, errors.New("SSH 私钥已加密，请提供私钥口令")
		}
		return nil, errors.New("SSH 私钥格式或口令无效")
	}
	return signer, nil
}

func referenceFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\\x00\r\n") {
		return "", errors.New("SSH 私钥 ID 无效")
	}
	return ReferencePrefix + id, nil
}
