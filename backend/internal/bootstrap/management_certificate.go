package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
)

const managementCertificateLifetime = 397 * 24 * time.Hour

type managementCertificateFileSnapshot struct {
	Data   []byte
	Mode   os.FileMode
	Exists bool
}

// ManagementCertificate 保存公网 IP 管理证书的落盘位置和回滚快照。
type ManagementCertificate struct {
	TargetIP        string
	Directory       string
	CertificatePath string
	FullChainPath   string
	PrivateKeyPath  string
	NotBefore       time.Time
	NotAfter        time.Time
	previous        map[string]managementCertificateFileSnapshot
}

// GenerateManagementCertificate 生成只覆盖指定公网 IP 的长期自签名管理证书。
func GenerateManagementCertificate(ctx context.Context, certificateRoot, target string) (result ManagementCertificate, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ip, err := normalizePublicManagementIP(target)
	if err != nil {
		return result, err
	}
	root := filepath.Clean(strings.TrimSpace(certificateRoot))
	if root == "." || root == "" || !filepath.IsAbs(root) {
		return result, errors.New("管理证书根目录必须是绝对路径")
	}
	directory := filepath.Join(root, "management-ip", managementIPDirectoryName(ip))
	if err := ensureManagementCertificateDirectory(directory); err != nil {
		return result, err
	}
	result = ManagementCertificate{
		TargetIP:        ip.String(),
		Directory:       directory,
		CertificatePath: filepath.Join(directory, "cert.pem"),
		FullChainPath:   filepath.Join(directory, "fullchain.pem"),
		PrivateKeyPath:  filepath.Join(directory, "key.pem"),
		previous:        make(map[string]managementCertificateFileSnapshot, 3),
	}
	for _, path := range managementCertificatePaths(result) {
		snapshot, snapshotErr := snapshotManagementCertificateFile(path)
		if snapshotErr != nil {
			return ManagementCertificate{}, snapshotErr
		}
		result.previous[path] = snapshot
	}
	committed := false
	defer func() {
		if committed || err == nil {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if rollbackErr := RollbackManagementCertificate(rollbackCtx, result); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("管理证书写入失败后的回滚未完成：%w", rollbackErr))
		}
	}()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return result, fmt.Errorf("生成管理证书私钥失败：%w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return result, fmt.Errorf("生成管理证书序列号失败：%w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Scert Management",
			Organization: []string{"Scert"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(managementCertificateLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{append(net.IP(nil), ip...)},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return result, fmt.Errorf("生成管理证书失败：%w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return result, fmt.Errorf("编码管理证书私钥失败：%w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	if _, err := tls.X509KeyPair(certificatePEM, privateKeyPEM); err != nil {
		return result, fmt.Errorf("管理证书与私钥不匹配：%w", err)
	}
	if err := atomicWriteManagementCertificate(result.CertificatePath, certificatePEM, 0o644); err != nil {
		return result, fmt.Errorf("写入管理证书失败：%w", err)
	}
	if err := atomicWriteManagementCertificate(result.FullChainPath, certificatePEM, 0o644); err != nil {
		return result, fmt.Errorf("写入管理证书链失败：%w", err)
	}
	if err := atomicWriteManagementCertificate(result.PrivateKeyPath, privateKeyPEM, 0o600); err != nil {
		return result, fmt.Errorf("写入管理证书私钥失败：%w", err)
	}
	if err := validateManagementCertificateFiles(ctx, result, ip); err != nil {
		return result, err
	}
	leaf, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		return result, fmt.Errorf("解析管理证书失败：%w", err)
	}
	result.NotBefore = leaf.NotBefore
	result.NotAfter = leaf.NotAfter
	committed = true
	return result, nil
}

// RollbackManagementCertificate 精确恢复生成前的三个管理证书文件。
func RollbackManagementCertificate(ctx context.Context, saved ManagementCertificate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var rollbackErrors []error
	for _, path := range managementCertificatePaths(saved) {
		if err := ctx.Err(); err != nil {
			rollbackErrors = append(rollbackErrors, err)
			break
		}
		snapshot, existed := saved.previous[path]
		if existed && snapshot.Exists {
			if err := atomicWriteManagementCertificate(path, snapshot.Data, snapshot.Mode); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("恢复管理证书文件 %s 失败：%w", filepath.Base(path), err))
			}
			continue
		}
		if err := removeRegularManagementCertificateFile(path); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

func normalizePublicManagementIP(value string) (net.IP, error) {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	ip := net.ParseIP(value)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return nil, fmt.Errorf("管理页面公网 IP 格式无效或不是可公开访问的单播地址：%s", value)
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return append(net.IP(nil), ipv4...), nil
	}
	return append(net.IP(nil), ip.To16()...), nil
}

func managementIPDirectoryName(ip net.IP) string {
	return strings.ReplaceAll(ip.String(), ":", "_")
}

func ensureManagementCertificateDirectory(directory string) error {
	for current := directory; ; current = filepath.Dir(current) {
		if info, err := os.Lstat(current); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("管理证书目录必须是普通目录：%s", current)
			}
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查管理证书目录失败：%w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建管理证书目录失败：%w", err)
	}
	return os.Chmod(directory, 0o700)
}

func snapshotManagementCertificateFile(path string) (managementCertificateFileSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return managementCertificateFileSnapshot{}, nil
	}
	if err != nil {
		return managementCertificateFileSnapshot{}, fmt.Errorf("检查管理证书文件失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return managementCertificateFileSnapshot{}, fmt.Errorf("管理证书目标必须是普通文件：%s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return managementCertificateFileSnapshot{}, fmt.Errorf("读取原管理证书文件失败：%w", err)
	}
	return managementCertificateFileSnapshot{Data: data, Mode: info.Mode().Perm(), Exists: true}, nil
}

func validateManagementCertificateFiles(ctx context.Context, saved ManagementCertificate, ip net.IP) error {
	for _, path := range managementCertificatePaths(saved) {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("管理证书必须是可读取的普通文件：%s", path)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(saved.PrivateKeyPath)
		if err != nil {
			return fmt.Errorf("检查管理证书私钥权限失败：%w", err)
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("管理证书私钥权限必须为 0600，当前为 %04o", info.Mode().Perm())
		}
	}
	if _, err := (certificate.LocalValidator{}).ValidateFiles(
		ctx,
		saved.CertificatePath,
		saved.FullChainPath,
		saved.PrivateKeyPath,
		[]string{ip.String()},
	); err != nil {
		return fmt.Errorf("管理证书文件校验失败：%w", err)
	}
	data, err := os.ReadFile(saved.CertificatePath)
	if err != nil {
		return fmt.Errorf("读取管理证书失败：%w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("管理证书 PEM 格式无效")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("解析管理证书失败：%w", err)
	}
	if len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(ip) {
		return errors.New("管理证书只能包含用户指定的单一 IP SAN")
	}
	publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return errors.New("管理证书必须使用 ECDSA P-256 公钥")
	}
	return nil
}

func atomicWriteManagementCertificate(path string, data []byte, mode os.FileMode) error {
	if _, err := snapshotManagementCertificateFile(path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".scert-management-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
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
		if err := removeRegularManagementCertificateFile(path); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func removeRegularManagementCertificateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查管理证书文件失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("拒绝删除非普通管理证书文件：%s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("删除管理证书文件失败：%w", err)
	}
	return nil
}

func managementCertificatePaths(saved ManagementCertificate) []string {
	return []string{saved.CertificatePath, saved.FullChainPath, saved.PrivateKeyPath}
}
