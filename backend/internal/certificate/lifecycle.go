package certificate

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Issuer interface {
	Obtain(ctx context.Context, request ObtainRequest) (Resource, error)
}

type ObtainRequest struct {
	Domains        []string
	AccountEmail   string
	Environment    string
	PreferredChain string
}

type Resource struct {
	Domains           []string
	Certificate       []byte
	IssuerCertificate []byte
	PrivateKey        []byte
	NotBefore         time.Time
	NotAfter          time.Time
}

type SavedResource struct {
	Domain          string    `json:"domain"`
	Directory       string    `json:"directory"`
	CertificatePath string    `json:"certificate_path"`
	FullChainPath   string    `json:"fullchain_path"`
	PrivateKeyPath  string    `json:"private_key_path"`
	NotBefore       time.Time `json:"not_before"`
	NotAfter        time.Time `json:"not_after"`
	BackupDirectory string    `json:"backup_directory,omitempty"`
}

type FileStore struct {
	Root       string
	BackupRoot string
	Now        func() time.Time
}

func NormalizeDomains(domains []string) ([]string, error) {
	seen := make(map[string]struct{}, len(domains))
	result := make([]string, 0, len(domains))
	for _, value := range domains {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "*.") {
			value = "*." + strings.TrimPrefix(value, "*.")
		}
		if !validDomain(value) {
			return nil, fmt.Errorf("证书域名无效：%s", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, errors.New("证书域名不能为空")
	}
	sort.Strings(result)
	return result, nil
}

func ValidateResource(resource Resource) (Resource, error) {
	if len(resource.Certificate) == 0 || len(resource.PrivateKey) == 0 {
		return Resource{}, errors.New("证书和私钥不能为空")
	}
	certificate, err := parseLeafCertificate(resource.Certificate)
	if err != nil {
		return Resource{}, fmt.Errorf("解析证书失败：%w", err)
	}
	if _, err := tls.X509KeyPair(resource.Certificate, resource.PrivateKey); err != nil {
		return Resource{}, fmt.Errorf("证书与私钥不匹配：%w", err)
	}
	domains, err := NormalizeDomains(resource.Domains)
	if err != nil {
		return Resource{}, err
	}
	if resource.NotBefore.IsZero() {
		resource.NotBefore = certificate.NotBefore
	}
	if resource.NotAfter.IsZero() {
		resource.NotAfter = certificate.NotAfter
	}
	if !resource.NotAfter.After(resource.NotBefore) {
		return Resource{}, errors.New("证书有效期无效")
	}
	resource.Domains = domains
	if len(resource.IssuerCertificate) > 0 {
		if _, err := parseLeafCertificate(resource.IssuerCertificate); err != nil {
			return Resource{}, fmt.Errorf("解析签发者证书失败：%w", err)
		}
	}
	return resource, nil
}

// ValidateResourceForDomains 校验证书内容，并确认叶子证书覆盖全部申请域名。
func ValidateResourceForDomains(resource Resource, expected []string) (Resource, error) {
	resource, err := ValidateResource(resource)
	if err != nil {
		return Resource{}, err
	}
	wanted, err := NormalizeDomains(expected)
	if err != nil {
		return Resource{}, err
	}
	leaf, err := parseLeafCertificate(resource.Certificate)
	if err != nil {
		return Resource{}, fmt.Errorf("解析证书失败：%w", err)
	}
	now := time.Now().UTC()
	if now.Before(leaf.NotBefore) {
		return Resource{}, fmt.Errorf("证书尚未生效：%s", leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if !now.Before(leaf.NotAfter) {
		return Resource{}, fmt.Errorf("证书已经过期：%s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	for _, name := range wanted {
		probe := name
		if strings.HasPrefix(probe, "*.") {
			probe = "scert-wildcard-probe." + strings.TrimPrefix(probe, "*.")
		}
		if err := leaf.VerifyHostname(probe); err != nil {
			return Resource{}, fmt.Errorf("证书不覆盖域名 %s：%w", name, err)
		}
	}
	return resource, nil
}

func (s FileStore) Save(ctx context.Context, domain string, resource Resource) (SavedResource, error) {
	if err := ctx.Err(); err != nil {
		return SavedResource{}, err
	}
	resource, err := ValidateResource(resource)
	if err != nil {
		return SavedResource{}, err
	}
	primary := strings.TrimSpace(domain)
	if primary == "" {
		primary = resource.Domains[0]
	}
	if _, err := NormalizeDomains([]string{primary}); err != nil {
		return SavedResource{}, err
	}
	root := filepath.Clean(strings.TrimSpace(s.Root))
	if root == "." || root == "" {
		return SavedResource{}, errors.New("证书存储根目录不能为空")
	}
	directory := filepath.Join(root, directoryName(primary))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return SavedResource{}, fmt.Errorf("创建证书目录失败：%w", err)
	}
	backupDirectory, err := s.backupExisting(directory, primary)
	if err != nil {
		return SavedResource{}, err
	}
	certificatePath := filepath.Join(directory, "cert.pem")
	fullChainPath := filepath.Join(directory, "fullchain.pem")
	privateKeyPath := filepath.Join(directory, "key.pem")
	fullChain := resource.Certificate
	if len(resource.IssuerCertificate) > 0 && !bytes.Contains(resource.Certificate, resource.IssuerCertificate) {
		fullChain = append(append([]byte(nil), resource.Certificate...), resource.IssuerCertificate...)
	}
	if err := atomicWrite(certificatePath, resource.Certificate, 0o644); err != nil {
		return SavedResource{}, fmt.Errorf("保存证书失败：%w", err)
	}
	if err := atomicWrite(fullChainPath, fullChain, 0o644); err != nil {
		return SavedResource{}, fmt.Errorf("保存完整证书链失败：%w", err)
	}
	if err := atomicWrite(privateKeyPath, resource.PrivateKey, 0o600); err != nil {
		return SavedResource{}, fmt.Errorf("保存私钥失败：%w", err)
	}
	result := SavedResource{
		Domain: primary, Directory: directory, CertificatePath: certificatePath,
		FullChainPath: fullChainPath, PrivateKeyPath: privateKeyPath,
		NotBefore: resource.NotBefore, NotAfter: resource.NotAfter,
		BackupDirectory: backupDirectory,
	}
	metadata, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return SavedResource{}, fmt.Errorf("生成证书元数据失败：%w", err)
	}
	if err := atomicWrite(filepath.Join(directory, "metadata.json"), metadata, 0o600); err != nil {
		return SavedResource{}, fmt.Errorf("保存证书元数据失败：%w", err)
	}
	return result, nil
}

func (s FileStore) backupExisting(directory, domain string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取原证书目录失败：%w", err)
	}
	if len(entries) == 0 {
		return "", nil
	}
	backupRoot := filepath.Clean(strings.TrimSpace(s.BackupRoot))
	if backupRoot == "." || backupRoot == "" {
		return "", errors.New("证书备份根目录不能为空")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	backupDirectory := filepath.Join(backupRoot, directoryName(domain), now().UTC().Format("20060102-150405.000000000"))
	if err := os.MkdirAll(filepath.Dir(backupDirectory), 0o700); err != nil {
		return "", fmt.Errorf("创建证书备份目录失败：%w", err)
	}
	if err := copyDirectory(directory, backupDirectory); err != nil {
		return "", fmt.Errorf("备份原证书失败：%w", err)
	}
	return backupDirectory, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".scert-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(temporaryName, path)
}

func copyDirectory(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(target, entry.Name()), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func parseLeafCertificate(data []byte) (*x509.Certificate, error) {
	remaining := data
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(block.Bytes)
	}
	return nil, errors.New("未找到 PEM 证书")
}

func directoryName(domain string) string {
	name := strings.ToLower(strings.TrimSpace(domain))
	name = strings.ReplaceAll(name, "*.", "_wildcard.")
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	return name
}

func validDomain(value string) bool {
	value = strings.TrimPrefix(value, "*.")
	if len(value) < 3 || len(value) > 253 || !strings.Contains(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}
