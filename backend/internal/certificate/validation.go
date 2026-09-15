package certificate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// ValidationResult 保存证书校验后的关键元数据。
type ValidationResult struct {
	Domains           []string  `json:"domains"`
	SerialNumber      string    `json:"serial_number"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	ChainLength       int       `json:"chain_length"`
}

// LocalValidator 校验本地证书、私钥和证书链。
type LocalValidator struct {
	Roots       *x509.CertPool
	Now         func() time.Time
	VerifyChain bool
}

// ValidateFiles 校验证书文件、私钥文件以及期望覆盖的域名。
func (v LocalValidator) ValidateFiles(ctx context.Context, certificatePath, fullChainPath, privateKeyPath string, expectedDomains []string) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	certificateData, err := os.ReadFile(certificatePath)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("读取证书文件失败：%w", err)
	}
	chainData := certificateData
	if strings.TrimSpace(fullChainPath) != "" {
		chainData, err = os.ReadFile(fullChainPath)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("读取完整证书链失败：%w", err)
		}
	}
	privateKeyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("读取私钥文件失败：%w", err)
	}
	pair, err := tls.X509KeyPair(certificateData, privateKeyData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("证书与私钥不匹配：%w", err)
	}
	chainPair, err := tls.X509KeyPair(chainData, privateKeyData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("证书与私钥不匹配：%w", err)
	}
	if !bytes.Equal(pair.Certificate[0], chainPair.Certificate[0]) {
		return ValidationResult{}, errors.New("证书文件与完整证书链的叶子证书不一致")
	}
	return v.validatePEM(ctx, chainData, expectedDomains)
}

// ValidatePEM 校验内存中的证书链和期望域名。
func (v LocalValidator) ValidatePEM(ctx context.Context, chainData []byte, expectedDomains []string) (ValidationResult, error) {
	return v.validatePEM(ctx, chainData, expectedDomains)
}

func (v LocalValidator) validatePEM(ctx context.Context, chainData []byte, expectedDomains []string) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	certificates, err := parseCertificates(chainData)
	if err != nil {
		return ValidationResult{}, err
	}
	leaf := certificates[0]
	wanted, err := normalizeValidationNames(expectedDomains)
	if err != nil {
		return ValidationResult{}, err
	}
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return ValidationResult{}, fmt.Errorf("证书当前不在有效期内：%s 至 %s", leaf.NotBefore.UTC().Format(time.RFC3339), leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	for _, name := range wanted {
		probe := name
		if strings.HasPrefix(probe, "*.") {
			probe = "scert-wildcard-probe." + strings.TrimPrefix(probe, "*.")
		}
		if err := leaf.VerifyHostname(probe); err != nil {
			return ValidationResult{}, fmt.Errorf("证书不覆盖域名 %s：%w", name, err)
		}
	}
	if v.VerifyChain {
		if err := verifyChain(certificates, v.Roots, now); err != nil {
			return ValidationResult{}, err
		}
	}
	return validationResult(leaf, len(certificates)), nil
}

// RemoteValidator 校验远程 HTTPS 服务实际返回的证书。
type RemoteValidator struct {
	Roots   *x509.CertPool
	Dialer  *net.Dialer
	Timeout time.Duration
	Now     func() time.Time
}

// ValidateHTTPS 连接目标 HTTPS 地址并校验证书链和域名。
func (v RemoteValidator) ValidateHTTPS(ctx context.Context, endpoint string, expectedDomains []string) (ValidationResult, error) {
	target, err := normalizeHTTPSURL(endpoint)
	if err != nil {
		return ValidationResult{}, err
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("解析 HTTPS 地址失败：%w", err)
	}
	address := parsed.Host
	if _, _, splitErr := net.SplitHostPort(address); splitErr != nil {
		address = net.JoinHostPort(parsed.Hostname(), "443")
	}
	dialer := v.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: v.timeout()}
	}
	ctx, cancel := context.WithTimeout(ctx, v.timeout())
	defer cancel()
	tlsDialer := &tls.Dialer{
		NetDialer: dialer,
		Config: &tls.Config{
			ServerName:         parsed.Hostname(),
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		},
	}
	connection, err := tlsDialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("remote HTTPS connection failed: %w", err)
	}
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		_ = connection.Close()
		return ValidationResult{}, errors.New("remote HTTPS connection type is invalid")
	}
	defer tlsConnection.Close()
	state := tlsConnection.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return ValidationResult{}, errors.New("远程 HTTPS 服务未返回证书")
	}
	certificates := make([]*x509.Certificate, len(state.PeerCertificates))
	copy(certificates, state.PeerCertificates)
	wanted := expectedDomains
	if len(wanted) == 0 {
		wanted = []string{parsed.Hostname()}
	}
	chainData := make([]byte, 0)
	for _, certificate := range certificates {
		chainData = append(chainData, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})...)
	}
	validator := LocalValidator{Roots: v.Roots, Now: v.Now, VerifyChain: true}
	return validator.ValidatePEM(ctx, chainData, wanted)
}

func (v RemoteValidator) timeout() time.Duration {
	if v.Timeout > 0 {
		return v.Timeout
	}
	return 10 * time.Second
}

func normalizeHTTPSURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("HTTPS 地址不能为空")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", errors.New("HTTPS 地址格式无效")
	}
	return parsed.String(), nil
}

func normalizeValidationNames(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if value == "" {
			continue
		}
		if net.ParseIP(strings.TrimPrefix(value, "*.")) == nil && !validDomain(value) {
			return nil, fmt.Errorf("证书校验域名无效：%s", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, errors.New("证书校验域名不能为空")
	}
	return result, nil
}
func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	remaining := data
	certificates := make([]*x509.Certificate, 0, 2)
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析证书链失败：%w", err)
		}
		certificates = append(certificates, certificate)
	}
	if len(certificates) == 0 {
		return nil, errors.New("未找到 PEM 证书链")
	}
	return certificates, nil
}

func verifyChain(certificates []*x509.Certificate, roots *x509.CertPool, now time.Time) error {
	if roots == nil {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range certificates[1:] {
		if !certificate.IsCA {
			continue
		}
		intermediates.AddCert(certificate)
	}
	_, err := certificates[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       "",
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("证书链校验失败：%w", err)
	}
	return nil
}

func validationResult(leaf *x509.Certificate, chainLength int) ValidationResult {
	return ValidationResult{
		Domains:           append([]string(nil), leaf.DNSNames...),
		SerialNumber:      leaf.SerialNumber.Text(16),
		FingerprintSHA256: fmt.Sprintf("%X", sha256.Sum256(leaf.Raw)),
		NotBefore:         leaf.NotBefore,
		NotAfter:          leaf.NotAfter,
		ChainLength:       chainLength,
	}
}
