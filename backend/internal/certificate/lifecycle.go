package certificate

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"strconv"
	"strings"
	"time"
)

type Issuer interface {
	Obtain(ctx context.Context, request ObtainRequest) (Resource, error)
}

type ObtainRequest struct {
	ChallengeType  string
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
	Domain                string    `json:"domain"`
	CertificateType       string    `json:"certificate_type,omitempty"`
	CertificateIdentifier string    `json:"certificate_identifier,omitempty"`
	Version               string    `json:"version,omitempty"`
	HistoryID             string    `json:"history_id,omitempty"`
	Current               bool      `json:"current"`
	SavedAt               time.Time `json:"saved_at"`
	SANs                  []string  `json:"sans,omitempty"`
	SerialNumber          string    `json:"serial_number,omitempty"`
	FingerprintSHA256     string    `json:"fingerprint_sha256,omitempty"`
	ChainLength           int       `json:"chain_length,omitempty"`
	Directory             string    `json:"directory"`
	CertificatePath       string    `json:"certificate_path"`
	FullChainPath         string    `json:"fullchain_path"`
	PrivateKeyPath        string    `json:"private_key_path"`
	NotBefore             time.Time `json:"not_before"`
	NotAfter              time.Time `json:"not_after"`
	BackupDirectory       string    `json:"backup_directory,omitempty"`
}

type FileStore struct {
	Root       string
	BackupRoot string
	Now        func() time.Time
	// 管理入口仍可能引用旧版本，完成双层 TLS 切换前不得清理该目录。
	RetainPrevious bool
	// 兼容仍引用业务目录的既有管理入口，禁止清理包含这些文件的旧版本。
	ProtectedPaths []string
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
	return s.save(ctx, domain, "", resource)
}

// SaveForType 按证书类型保存唯一的当前证书版本。
func (s FileStore) SaveForType(ctx context.Context, domain, certificateType string, resource Resource) (SavedResource, error) {
	return s.save(ctx, domain, certificateType, resource)
}

func (s FileStore) save(ctx context.Context, domain, certificateType string, resource Resource) (SavedResource, error) {
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
	baseDirectory := filepath.Join(root, directoryName(primary))
	savedAt := time.Now().UTC()
	if s.Now != nil {
		savedAt = s.Now().UTC()
	}
	typeDirectory := baseDirectory
	directory := baseDirectory
	certificateIdentifier := ""
	if certificateType != "" {
		certificateType = strings.ToLower(strings.TrimSpace(certificateType))
		if certificateType != "single" && certificateType != "wildcard" && certificateType != "san" {
			return SavedResource{}, fmt.Errorf("证书类型无效：%s", certificateType)
		}
		typeDirectory = filepath.Join(baseDirectory, certificateType)
		directory = typeDirectory
		if certificateType == "san" {
			certificateIdentifier = sanCertificateIdentifier(resource.Domains)
			directory = filepath.Join(typeDirectory, certificateIdentifier)
		}
	}
	activeDirectory := directory
	currentDirectory := directory
	version := ""
	if certificateType != "" {
		activeDirectory, err = s.activeCertificateDirectoryForType(typeDirectory, certificateType)
		if err != nil {
			return SavedResource{}, err
		}
		if !directoryContainsCertificateFiles(activeDirectory) {
			activeDirectory = ""
		}
		version, err = nextCertificateVersion(directory, savedAt)
		if err != nil {
			return SavedResource{}, err
		}
		// SAN 组合变化或系统时间回拨时，新当前版本仍必须排在旧版本之后。
		if activeDirectory != "" && !newerCertificateDirectory(version, filepath.Base(activeDirectory)) {
			date, major, minor, valid := parseCertificateVersion(filepath.Base(activeDirectory))
			if valid {
				version = fmt.Sprintf("%s-V%d.%d", date, major, minor+1)
			}
		}
		currentDirectory = filepath.Join(directory, version)
	}
	protected := false
	if !s.RetainPrevious && certificateType != "" && activeDirectory != "" {
		protected, err = s.isProtectedDirectory(activeDirectory)
		if err != nil {
			return SavedResource{}, fmt.Errorf("检查旧证书引用失败：%w", err)
		}
	}
	backupDirectory := ""
	if err := validateCertificateWritePath(currentDirectory); err != nil {
		return SavedResource{}, err
	}
	if activeDirectory != "" {
		if err := validateCertificateWritePath(activeDirectory); err != nil {
			return SavedResource{}, err
		}
		backupDirectory, err = s.backupExisting(activeDirectory, primary)
		if err != nil {
			return SavedResource{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(currentDirectory), 0o700); err != nil {
		return SavedResource{}, fmt.Errorf("创建证书目录失败：%w", err)
	}
	if certificateType != "" {
		if err := os.Mkdir(currentDirectory, 0o700); err != nil {
			return SavedResource{}, fmt.Errorf("创建新证书版本失败：%w", err)
		}
	} else if err := os.MkdirAll(currentDirectory, 0o700); err != nil {
		return SavedResource{}, fmt.Errorf("创建证书目录失败：%w", err)
	}
	fullChain := resource.Certificate
	if len(resource.IssuerCertificate) > 0 && !bytes.Contains(resource.Certificate, resource.IssuerCertificate) {
		fullChain = append(append([]byte(nil), resource.Certificate...), resource.IssuerCertificate...)
	}
	if err := writeCertificateFiles(currentDirectory, resource.Certificate, fullChain, resource.PrivateKey); err != nil {
		if certificateType != "" {
			_ = os.RemoveAll(currentDirectory)
		}
		return SavedResource{}, err
	}
	leaf, _ := parseLeafCertificate(resource.Certificate)
	result := SavedResource{
		Domain: primary, CertificateType: certificateType, CertificateIdentifier: certificateIdentifier,
		Version: version, SavedAt: savedAt, SANs: append([]string(nil), resource.Domains...),
		Directory: currentDirectory, CertificatePath: filepath.Join(currentDirectory, "cert.pem"),
		FullChainPath: filepath.Join(currentDirectory, "fullchain.pem"), PrivateKeyPath: filepath.Join(currentDirectory, "key.pem"),
		NotBefore: resource.NotBefore, NotAfter: resource.NotAfter, BackupDirectory: backupDirectory,
		ChainLength: certificateChainLength(fullChain),
	}
	if leaf != nil {
		result.SerialNumber = leaf.SerialNumber.Text(16)
		result.FingerprintSHA256 = fmt.Sprintf("%X", sha256.Sum256(leaf.Raw))
	}
	metadata, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		if certificateType != "" {
			_ = os.RemoveAll(currentDirectory)
		}
		return SavedResource{}, fmt.Errorf("生成证书元数据失败：%w", err)
	}
	if err := atomicWrite(filepath.Join(currentDirectory, "metadata.json"), metadata, 0o600); err != nil {
		if certificateType != "" {
			_ = os.RemoveAll(currentDirectory)
		}
		return SavedResource{}, fmt.Errorf("写入证书元数据失败：%w", err)
	}
	if !s.RetainPrevious && !protected && certificateType != "" && activeDirectory != "" && activeDirectory != currentDirectory {
		if err := removeCertificateDirectory(activeDirectory, typeDirectory); err != nil {
			return result, fmt.Errorf("清理旧当前证书目录失败：%w", err)
		}
	}
	return result, nil
}

func writeCertificateFiles(directory string, certificate, fullChain, privateKey []byte) error {
	if err := atomicWrite(filepath.Join(directory, "cert.pem"), certificate, 0o644); err != nil {
		return fmt.Errorf("写入证书文件失败：%w", err)
	}
	if err := atomicWrite(filepath.Join(directory, "fullchain.pem"), fullChain, 0o644); err != nil {
		return fmt.Errorf("写入完整证书链失败：%w", err)
	}
	if err := atomicWrite(filepath.Join(directory, "key.pem"), privateKey, 0o600); err != nil {
		return fmt.Errorf("写入私钥文件失败：%w", err)
	}
	return nil
}

func certificateChainLength(data []byte) int {
	count := 0
	remaining := data
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = rest
		if block.Type == "CERTIFICATE" {
			count++
		}
	}
	return count
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
	backupBase := filepath.Join(backupRoot, directoryName(domain), now().UTC().Format("20060102-150405.000000000"))
	backupDirectory := backupBase
	for suffix := 1; ; suffix++ {
		if _, statErr := os.Stat(backupDirectory); errors.Is(statErr, os.ErrNotExist) {
			break
		} else if statErr != nil {
			return "", fmt.Errorf("读取证书备份目录失败：%w", statErr)
		}
		backupDirectory = fmt.Sprintf("%s-%02d", backupBase, suffix)
	}
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

func nextCertificateVersion(directory string, now time.Time) (string, error) {
	date := now.UTC().Format("20060102")
	major := 1
	minor := -1
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("读取证书类型目录失败：%w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryDate, entryMajor, entryMinor, valid := parseCertificateVersion(entry.Name())
		if !valid || entryDate != date {
			continue
		}
		if entryMajor > major || entryMajor == major && entryMinor > minor {
			major = entryMajor
			minor = entryMinor
		}
	}
	if minor < 0 {
		minor = 0
	} else {
		minor++
	}
	return fmt.Sprintf("%s-V%d.%d", date, major, minor), nil
}

func parseCertificateVersion(version string) (string, int, int, bool) {
	if len(version) < len("20060102-V1.0") || filepath.Base(version) != version || strings.ContainsAny(version, `/\\`) {
		return "", 0, 0, false
	}
	if version[8:10] != "-V" {
		return "", 0, 0, false
	}
	date := version[:8]
	for _, r := range date {
		if r < '0' || r > '9' {
			return "", 0, 0, false
		}
	}
	parts := strings.Split(version[10:], ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 1 {
		return "", 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return "", 0, 0, false
	}
	return date, major, minor, true
}

func (s FileStore) activeCertificateDirectory(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return directory, nil
		}
		return "", fmt.Errorf("读取证书类型目录失败：%w", err)
	}
	active := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(directory, entry.Name())
		if _, _, _, valid := parseCertificateVersion(entry.Name()); valid && newerCertificateDirectory(candidate, active) {
			active = candidate
		}
	}
	if active != "" {
		return active, nil
	}
	legacyCurrent := filepath.Join(directory, "current")
	if info, statErr := os.Stat(legacyCurrent); statErr == nil && info.IsDir() {
		return legacyCurrent, nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("读取旧版当前证书目录失败：%w", statErr)
	}
	return directory, nil
}

func sanCertificateIdentifier(domains []string) string {
	sum := sha256.Sum256([]byte(strings.Join(domains, "\n")))
	return fmt.Sprintf("san-%x", sum[:8])
}

func directoryContainsCertificateFiles(directory string) bool {
	if strings.TrimSpace(directory) == "" {
		return false
	}
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func newerCertificateDirectory(candidate, current string) bool {
	if current == "" {
		return true
	}
	candidateDate, candidateMajor, candidateMinor, candidateValid := parseCertificateVersion(filepath.Base(candidate))
	currentDate, currentMajor, currentMinor, currentValid := parseCertificateVersion(filepath.Base(current))
	if !candidateValid {
		return false
	}
	if !currentValid {
		return true
	}
	if candidateDate != currentDate {
		return candidateDate > currentDate
	}
	if candidateMajor != currentMajor {
		return candidateMajor > currentMajor
	}
	if candidateMinor != currentMinor {
		return candidateMinor > currentMinor
	}
	return candidate > current
}

func (s FileStore) activeCertificateDirectoryForType(typeDirectory, certificateType string) (string, error) {
	active, err := s.activeCertificateDirectory(typeDirectory)
	if err != nil {
		return "", err
	}
	if certificateType != "san" || directoryContainsCertificateFiles(active) {
		return active, nil
	}
	entries, err := os.ReadDir(typeDirectory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return typeDirectory, nil
		}
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "current" {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "san-") {
			continue
		}
		if _, _, _, valid := parseCertificateVersion(entry.Name()); valid {
			continue
		}
		candidate, candidateErr := s.activeCertificateDirectory(filepath.Join(typeDirectory, entry.Name()))
		if candidateErr != nil {
			return "", candidateErr
		}
		if directoryContainsCertificateFiles(candidate) && newerCertificateDirectory(candidate, active) {
			active = candidate
		}
	}
	return active, nil
}
func removeCertificateDirectory(directory, certificateTypeDirectory string) error {
	directory = filepath.Clean(directory)
	certificateTypeDirectory = filepath.Clean(certificateTypeDirectory)
	parentDirectory := filepath.Dir(directory)
	if parentDirectory != certificateTypeDirectory && filepath.Dir(parentDirectory) != certificateTypeDirectory {
		return errors.New("旧证书目录超出证书类型目录")
	}
	name := filepath.Base(directory)
	if name != "current" {
		if err := validateCertificateVersion(name); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(directory); err != nil {
		return err
	}
	if parentDirectory != certificateTypeDirectory {
		entries, err := os.ReadDir(parentDirectory)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if len(entries) == 0 {
			return os.Remove(parentDirectory)
		}
	}
	return nil
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

// CurrentDirectory 返回当前唯一使用的证书版本目录。
func (s FileStore) CurrentDirectory(domain, certificateType string) (string, error) {
	primary := strings.TrimSpace(domain)
	if _, err := NormalizeDomains([]string{primary}); err != nil {
		return "", err
	}
	certificateType = strings.ToLower(strings.TrimSpace(certificateType))
	if certificateType != "single" && certificateType != "wildcard" && certificateType != "san" {
		return "", fmt.Errorf("证书类型无效：%s", certificateType)
	}
	root := filepath.Clean(strings.TrimSpace(s.Root))
	if root == "." || root == "" {
		return "", errors.New("证书存储根目录不能为空")
	}
	directory := filepath.Join(root, directoryName(primary), certificateType)
	return s.activeCertificateDirectoryForType(directory, certificateType)
}

// LoadCurrent reads the active certificate files and metadata.
func (s FileStore) LoadCurrent(ctx context.Context, domain, certificateType string) (SavedResource, Resource, error) {
	if err := ctx.Err(); err != nil {
		return SavedResource{}, Resource{}, err
	}
	directory, err := s.CurrentDirectory(domain, certificateType)
	if err != nil {
		return SavedResource{}, Resource{}, err
	}
	certificateData, err := os.ReadFile(filepath.Join(directory, "cert.pem"))
	if err != nil {
		return SavedResource{}, Resource{}, fmt.Errorf("读取证书文件失败：%w", err)
	}
	fullChainData, err := os.ReadFile(filepath.Join(directory, "fullchain.pem"))
	if err != nil {
		return SavedResource{}, Resource{}, fmt.Errorf("读取完整证书链失败：%w", err)
	}
	privateKeyData, err := os.ReadFile(filepath.Join(directory, "key.pem"))
	if err != nil {
		return SavedResource{}, Resource{}, fmt.Errorf("读取私钥文件失败：%w", err)
	}
	resource, err := ValidateResourceForDomains(Resource{
		Domains:           []string{domain},
		Certificate:       certificateData,
		IssuerCertificate: fullChainData,
		PrivateKey:        privateKeyData,
	}, []string{domain})
	if err != nil {
		return SavedResource{}, Resource{}, err
	}
	resource.IssuerCertificate = nil
	var saved SavedResource
	metadata, metadataErr := os.ReadFile(filepath.Join(directory, "metadata.json"))
	if metadataErr == nil {
		if err := json.Unmarshal(metadata, &saved); err != nil {
			return SavedResource{}, Resource{}, fmt.Errorf("解析证书元数据失败：%w", err)
		}
	}
	if saved.Domain == "" {
		saved = SavedResource{
			Domain:          domain,
			CertificateType: certificateType,
			Directory:       directory,
			CertificatePath: filepath.Join(directory, "cert.pem"),
			FullChainPath:   filepath.Join(directory, "fullchain.pem"),
			PrivateKeyPath:  filepath.Join(directory, "key.pem"),
			NotBefore:       resource.NotBefore,
			NotAfter:        resource.NotAfter,
			SANs:            resource.Domains,
			ChainLength:     certificateChainLength(fullChainData),
		}
	}
	return saved, resource, nil
}

// LoadVersion 读取可唯一识别的当前或历史版本，恢复前仍需校验当前域名配置。
func (s FileStore) LoadVersion(ctx context.Context, domain, certificateType, version string) (SavedResource, Resource, error) {
	return s.loadHistoryVersion(ctx, domain, certificateType, version)
}

// RestoreVersion 从本地历史归档创建新的当前版本，不执行远端部署。
func (s FileStore) RestoreVersion(ctx context.Context, domain, certificateType, version string) (SavedResource, error) {
	_, resource, err := s.LoadVersion(ctx, domain, certificateType, version)
	if err != nil {
		return SavedResource{}, err
	}
	return s.restoreResource(ctx, domain, certificateType, resource, resource.Domains, nil)
}

// ListVersions 列出当前版本和本地归档，过期版本可展示但不可恢复。
func (s FileStore) ListVersions(ctx context.Context, domain, certificateType string) ([]SavedResource, error) {
	entries, err := s.historyEntries(ctx, domain, certificateType)
	if err != nil {
		return nil, err
	}
	items := make([]SavedResource, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entry.saved)
	}
	return items, nil
}

func validateCertificateVersion(version string) error {
	if _, _, _, valid := parseCertificateVersion(version); !valid {
		return errors.New("证书版本标识无效，应使用 YYYYMMDD-Vx.x 格式")
	}
	return nil
}

func removeCertificateFiles(directory string) error {
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// RollbackSavedResource 在初始化后续阶段失败时恢复证书目录。
// 如果保存前存在旧证书，则从备份目录恢复；否则仅清理本次写入的固定证书文件。
func RollbackSavedResource(ctx context.Context, saved SavedResource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Clean(strings.TrimSpace(saved.Directory))
	if directory == "." || directory == "" {
		return errors.New("证书回滚目录不能为空")
	}
	if strings.TrimSpace(saved.BackupDirectory) != "" {
		backupDirectory := filepath.Clean(strings.TrimSpace(saved.BackupDirectory))
		if backupDirectory == "." || backupDirectory == "" {
			return errors.New("证书备份目录无效")
		}
		if _, err := os.Stat(backupDirectory); err != nil {
			return fmt.Errorf("读取证书备份目录失败：%w", err)
		}
		if err := restoreCertificateFiles(directory, backupDirectory); err != nil {
			return fmt.Errorf("恢复原证书失败：%w", err)
		}
		if err := rewriteRestoredMetadata(directory); err != nil {
			return fmt.Errorf("更新回滚证书元数据失败：%w", err)
		}
		return nil
	}
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		path := filepath.Join(directory, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("清理失败证书文件 %s 失败：%w", name, err)
		}
	}
	if _, _, _, valid := parseCertificateVersion(filepath.Base(directory)); valid {
		if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("清理失败证书目录失败：%w", err)
		}
	}
	return nil
}

func restoreCertificateFiles(directory, backupDirectory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"cert.pem", "fullchain.pem", "key.pem", "metadata.json"} {
		source := filepath.Join(backupDirectory, name)
		data, err := os.ReadFile(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		mode := os.FileMode(0o600)
		if name == "cert.pem" || name == "fullchain.pem" {
			mode = 0o644
		}
		if err := atomicWrite(filepath.Join(directory, name), data, mode); err != nil {
			return err
		}
	}
	return nil
}

func rewriteRestoredMetadata(directory string) error {
	path := filepath.Join(directory, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var saved SavedResource
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	saved.Directory = directory
	saved.CertificatePath = filepath.Join(directory, "cert.pem")
	saved.FullChainPath = filepath.Join(directory, "fullchain.pem")
	saved.PrivateKeyPath = filepath.Join(directory, "key.pem")
	saved.BackupDirectory = ""
	data, err = json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o600)
}
