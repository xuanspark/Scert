package bootstrap

import (
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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const temporaryCertificateLifetime = 24 * time.Hour

var interfaceAddresses = net.InterfaceAddrs

type TLSMaterial struct {
	Certificate tls.Certificate
	Leaf        *x509.Certificate
	IPAddresses []net.IP
}

// NewTemporaryTLSMaterial 生成仅在当前 Bootstrap 进程内使用的临时自签名证书。
func NewTemporaryTLSMaterial(explicitPublicIP string) (TLSMaterial, error) {
	ips, err := bootstrapCertificateIPs(explicitPublicIP)
	if err != nil {
		return TLSMaterial{}, err
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TLSMaterial{}, fmt.Errorf("生成 Bootstrap 临时私钥失败：%w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return TLSMaterial{}, fmt.Errorf("生成 Bootstrap 临时证书序列号失败：%w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Scert Bootstrap",
			Organization: []string{"Scert"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(temporaryCertificateLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           cloneIPs(ips),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return TLSMaterial{}, fmt.Errorf("生成 Bootstrap 临时证书失败：%w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return TLSMaterial{}, fmt.Errorf("编码 Bootstrap 临时私钥失败：%w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return TLSMaterial{}, fmt.Errorf("装载 Bootstrap 临时证书失败：%w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return TLSMaterial{}, fmt.Errorf("解析 Bootstrap 临时证书失败：%w", err)
	}
	certificate.Leaf = leaf
	return TLSMaterial{Certificate: certificate, Leaf: leaf, IPAddresses: cloneIPs(ips)}, nil
}

func bootstrapCertificateIPs(explicitPublicIP string) ([]net.IP, error) {
	result := make([]net.IP, 0, 8)
	seen := make(map[string]struct{})
	appendIP := func(ip net.IP) {
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			ip = ipv4
		} else {
			ip = ip.To16()
		}
		if ip == nil {
			return
		}
		key := ip.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result = append(result, append(net.IP(nil), ip...))
	}
	appendIP(net.ParseIP("127.0.0.1"))
	appendIP(net.ParseIP("::1"))
	addresses, err := interfaceAddresses()
	if err != nil {
		return nil, fmt.Errorf("读取本机网络地址失败：%w", err)
	}
	for _, address := range addresses {
		var raw string
		switch value := address.(type) {
		case *net.IPNet:
			raw = value.IP.String()
		case *net.IPAddr:
			raw = value.IP.String()
		default:
			raw = address.String()
			if host, _, splitErr := net.SplitHostPort(raw); splitErr == nil {
				raw = host
			} else if index := strings.LastIndex(raw, "/"); index > 0 {
				raw = raw[:index]
			}
		}
		appendIP(net.ParseIP(strings.Trim(strings.TrimSpace(raw), "[]")))
	}
	if value := strings.TrimSpace(explicitPublicIP); value != "" {
		ip := net.ParseIP(strings.Trim(value, "[]"))
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, fmt.Errorf("Bootstrap 公网 IP 格式无效：%s", value)
		}
		appendIP(ip)
	}
	return result, nil
}

func advertisedBootstrapIP(explicitPublicIP string, certificateIPs []net.IP) (net.IP, error) {
	if value := strings.TrimSpace(explicitPublicIP); value != "" {
		ip := net.ParseIP(strings.Trim(value, "[]"))
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, fmt.Errorf("Bootstrap 公网 IP 格式无效：%s", value)
		}
		return append(net.IP(nil), ip...), nil
	}
	for _, ip := range certificateIPs {
		if ip != nil && !ip.IsLoopback() && ip.To4() != nil {
			return append(net.IP(nil), ip...), nil
		}
	}
	for _, ip := range certificateIPs {
		if ip != nil && !ip.IsLoopback() {
			return append(net.IP(nil), ip...), nil
		}
	}
	return net.ParseIP("127.0.0.1"), nil
}

func bootstrapURL(ip net.IP, port int, token string) (string, error) {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return "", errors.New("Bootstrap 访问 IP 无效")
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("Bootstrap 访问端口无效：%d", port)
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("Bootstrap 一次性令牌不能为空")
	}
	value := &url.URL{
		Scheme:   "https",
		Host:     net.JoinHostPort(ip.String(), strconv.Itoa(port)),
		Path:     "/",
		Fragment: "token=" + url.QueryEscape(token),
	}
	return value.String(), nil
}

// WriteBootstrapURL 以原子替换方式写入仅管理员可读的一次性初始化地址。
func WriteBootstrapURL(path, value string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return errors.New("Bootstrap 地址文件必须使用绝对路径")
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("Bootstrap 地址不能为空")
	}
	directory := filepath.Dir(path)
	if directory == filepath.VolumeName(directory)+string(filepath.Separator) {
		return errors.New("Bootstrap 地址文件不能直接放在文件系统根目录")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建 Bootstrap 运行目录失败：%w", err)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Bootstrap 运行目录必须是普通目录")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("设置 Bootstrap 运行目录权限失败：%w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Bootstrap 地址文件不能是符号链接")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 Bootstrap 地址文件失败：%w", err)
	}
	temporary, err := os.CreateTemp(directory, ".bootstrap-url-")
	if err != nil { return fmt.Errorf("创建 Bootstrap 临时地址文件失败：%w", err) }
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	defer temporary.Close()
	if _, err := temporary.WriteString(value+"\n"); err != nil {
		return fmt.Errorf("写入 Bootstrap 临时地址文件失败：%w", err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("设置 Bootstrap 临时地址文件权限失败：%w", err)
	}
	if err := temporary.Sync(); err != nil { return fmt.Errorf("同步 Bootstrap 地址文件失败：%w", err) }
	if err := temporary.Close(); err != nil { return fmt.Errorf("关闭 Bootstrap 地址文件失败：%w", err) }
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("替换 Bootstrap 地址文件失败：%w", err)
	}
	return nil
}

func cloneIPs(values []net.IP) []net.IP {
	result := make([]net.IP, 0, len(values))
	for _, value := range values {
		result = append(result, append(net.IP(nil), value...))
	}
	return result
}
