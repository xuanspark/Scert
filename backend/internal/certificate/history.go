package certificate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type historyEntry struct {
	saved    SavedResource
	resource Resource
}

var errHistoryOtherType = errors.New("归档属于其他证书类型")

// openHistoryRoot 拒绝符号链接目录，并将所有后续读取限制在已打开的存储根内。
func openHistoryRoot(path string) (*os.Root, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("证书存储根目录不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("证书存储路径必须为真实目录")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return os.OpenRoot(absolute)
}

func openHistoryDirectory(root *os.Root, relative string) (*os.Root, error) {
	if !filepath.IsLocal(relative) {
		return nil, errors.New("证书历史路径超出存储目录")
	}
	current := ""
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("证书历史目录不允许符号链接")
		}
	}
	return root.OpenRoot(relative)
}

func readHistoryFile(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	const maximumFileSize = 4 << 20
	if !info.Mode().IsRegular() || info.Size() > maximumFileSize {
		return nil, errors.New("证书历史文件必须为大小受限的普通文件")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumFileSize {
		return nil, errors.New("证书历史文件超过大小限制")
	}
	return data, nil
}

func readHistoryEntry(root *os.Root, relative, domain, certificateType string) (historyEntry, error) {
	directory, err := openHistoryDirectory(root, relative)
	if err != nil {
		return historyEntry{}, err
	}
	defer directory.Close()
	metadata, err := readHistoryFile(directory, "metadata.json")
	if err != nil {
		return historyEntry{}, err
	}
	var saved SavedResource
	if err := json.Unmarshal(metadata, &saved); err != nil {
		return historyEntry{}, fmt.Errorf("解析历史证书元数据失败：%w", err)
	}
	if saved.CertificateType != certificateType {
		return historyEntry{}, errHistoryOtherType
	}
	if directoryName(saved.Domain) != directoryName(domain) {
		return historyEntry{}, errors.New("历史证书归属域名不匹配")
	}
	if saved.Version != "" {
		if err := validateCertificateVersion(saved.Version); err != nil {
			return historyEntry{}, err
		}
	}
	certificateData, err := readHistoryFile(directory, "cert.pem")
	if err != nil {
		return historyEntry{}, err
	}
	chain, err := readHistoryFile(directory, "fullchain.pem")
	if err != nil {
		return historyEntry{}, err
	}
	key, err := readHistoryFile(directory, "key.pem")
	if err != nil {
		return historyEntry{}, err
	}
	names := saved.SANs
	if len(names) == 0 {
		names = []string{domain}
	}
	chainLeafData, issuerData, err := splitCertificateChain(chain)
	if err != nil {
		return historyEntry{}, err
	}
	resource, err := ValidateResource(Resource{Domains: names, Certificate: certificateData, IssuerCertificate: issuerData, PrivateKey: key})
	if err != nil {
		return historyEntry{}, err
	}
	leaf, err := parseLeafCertificate(certificateData)
	if err != nil {
		return historyEntry{}, err
	}
	chainLeaf, err := parseLeafCertificate(chainLeafData)
	if err != nil || !bytes.Equal(leaf.Raw, chainLeaf.Raw) {
		return historyEntry{}, errors.New("历史证书与完整链的叶子证书不一致")
	}
	for _, name := range append(append([]string(nil), resource.Domains...), domain) {
		probe := strings.Replace(name, "*.", "scert-wildcard-probe.", 1)
		if err := leaf.VerifyHostname(probe); err != nil {
			return historyEntry{}, fmt.Errorf("历史证书不覆盖域名 %s：%w", name, err)
		}
	}
	remaining := chain
	chainLength := 0
	for len(bytes.TrimSpace(remaining)) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" {
			return historyEntry{}, errors.New("历史证书完整链格式无效")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return historyEntry{}, fmt.Errorf("解析历史证书链失败：%w", err)
		}
		chainLength++
		remaining = rest
	}
	// 元数据中的路径、指纹、有效期均不可信，必须从实际文件重新生成。
	saved.Domain = domain
	saved.SANs = append([]string(nil), resource.Domains...)
	saved.NotBefore, saved.NotAfter = leaf.NotBefore, leaf.NotAfter
	saved.SerialNumber = leaf.SerialNumber.Text(16)
	saved.FingerprintSHA256 = fmt.Sprintf("%X", sha256.Sum256(leaf.Raw))
	saved.ChainLength = chainLength
	saved.CertificateIdentifier = ""
	if certificateType == "san" {
		saved.CertificateIdentifier = sanCertificateIdentifier(resource.Domains)
	}
	saved.Directory = filepath.Join(root.Name(), relative)
	saved.CertificatePath = filepath.Join(saved.Directory, "cert.pem")
	saved.FullChainPath = filepath.Join(saved.Directory, "fullchain.pem")
	saved.PrivateKeyPath = filepath.Join(saved.Directory, "key.pem")
	saved.BackupDirectory = ""
	saved.Current = false
	identity := fmt.Sprintf("%s\n%s\n%s\n%s\n%x", directoryName(domain), certificateType, saved.Version, strings.Join(resource.Domains, ","), sha256.Sum256(chain))
	saved.HistoryID = fmt.Sprintf("history-%x", sha256.Sum256([]byte(identity)))
	return historyEntry{saved: saved, resource: resource}, nil
}

func splitCertificateChain(data []byte) ([]byte, []byte, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, errors.New("历史证书完整链缺少叶子证书")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return nil, nil, fmt.Errorf("解析历史证书叶子失败：%w", err)
	}
	issuer := append([]byte(nil), rest...)
	if len(bytes.TrimSpace(issuer)) == 0 {
		issuer = nil
	}
	return pem.EncodeToMemory(block), issuer, nil
}

func (s FileStore) historyEntries(ctx context.Context, domain, certificateType string) ([]historyEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.CurrentDirectory(domain, certificateType)
	if err != nil {
		return nil, err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	certificateType = strings.ToLower(strings.TrimSpace(certificateType))
	entries := make([]historyEntry, 0)
	seen := make(map[string]bool)
	appendEntry := func(entry historyEntry) {
		if !seen[entry.saved.HistoryID] {
			seen[entry.saved.HistoryID] = true
			entries = append(entries, entry)
		}
	}
	root, err := openHistoryRoot(s.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		defer root.Close()
		absolute, err := filepath.Abs(current)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root.Name(), absolute)
		if err != nil {
			return nil, err
		}
		entry, err := readHistoryEntry(root, relative, domain, certificateType)
		if err == nil {
			entry.saved.Current = true
			appendEntry(entry)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("读取当前证书版本失败：%w", err)
		}
	}
	if strings.TrimSpace(s.BackupRoot) == "" {
		return entries, nil
	}
	backups, err := openHistoryRoot(s.BackupRoot)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	defer backups.Close()
	directory, err := openHistoryDirectory(backups, directoryName(domain))
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	file, err := directory.Open(".")
	if err != nil {
		return nil, err
	}
	children, err := file.ReadDir(-1)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if child.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("历史证书归档不允许符号链接")
		}
		if !child.IsDir() {
			continue
		}
		entry, err := readHistoryEntry(directory, child.Name(), domain, certificateType)
		if errors.Is(err, errHistoryOtherType) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读取历史证书归档失败：%w", err)
		}
		appendEntry(entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].saved.Current != entries[j].saved.Current {
			return entries[i].saved.Current
		}
		if !entries[i].saved.SavedAt.Equal(entries[j].saved.SavedAt) {
			return entries[i].saved.SavedAt.After(entries[j].saved.SavedAt)
		}
		return entries[i].saved.HistoryID < entries[j].saved.HistoryID
	})
	return entries, nil
}

func (s FileStore) loadHistoryVersion(ctx context.Context, domain, certificateType, version string) (SavedResource, Resource, error) {
	if strings.HasPrefix(version, "history-") {
		digest, err := hex.DecodeString(strings.TrimPrefix(version, "history-"))
		if err != nil || len(digest) != sha256.Size {
			return SavedResource{}, Resource{}, errors.New("证书历史标识无效")
		}
	} else if err := validateCertificateVersion(version); err != nil {
		return SavedResource{}, Resource{}, err
	}
	entries, err := s.historyEntries(ctx, domain, certificateType)
	if err != nil {
		return SavedResource{}, Resource{}, err
	}
	var selected *historyEntry
	for index := range entries {
		entry := &entries[index]
		if entry.saved.HistoryID != version && entry.saved.Version != version {
			continue
		}
		if selected != nil {
			return SavedResource{}, Resource{}, errors.New("证书版本号存在歧义，请使用历史标识恢复")
		}
		selected = entry
	}
	if selected == nil {
		return SavedResource{}, Resource{}, errors.New("未找到指定证书历史版本")
	}
	resource, err := ValidateResourceForDomains(selected.resource, selected.resource.Domains)
	if err != nil {
		return SavedResource{}, Resource{}, fmt.Errorf("历史证书不可恢复：%w", err)
	}
	return selected.saved, resource, nil
}
