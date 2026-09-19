package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
)

type ManagementCertificateState struct {
	Hostname        string
	CertificatePath string
	PrivateKeyPath  string
	NotAfter        time.Time
	HTTPSMode       string
	ManagementURL   string
	DNSVerified     bool
	Request         certificate.SetupCertificateRequest
}

// ManagementCertificate 返回实际配置的管理证书，不依赖业务域名仓储。
func (s *Server) ManagementCertificate() (ManagementCertificateState, error) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	state := s.managementCertificateState()
	pair, err := tls.LoadX509KeyPair(state.CertificatePath, state.PrivateKeyPath)
	if err != nil {
		return state, fmt.Errorf("读取管理证书失败：%w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return state, fmt.Errorf("解析管理证书失败：%w", err)
	}
	state.NotAfter = leaf.NotAfter
	return state, nil
}

// 调用方持有配置锁；只返回续期需要的值，不暴露完整配置或通知凭据。
func (s *Server) managementCertificateState() ManagementCertificateState {
	return ManagementCertificateState{
		Hostname:        strings.TrimSpace(s.config.Web.Hostname),
		CertificatePath: s.config.Server.TLSCert,
		PrivateKeyPath:  s.config.Server.TLSKey,
		HTTPSMode:       s.config.Server.HTTPSMode,
		ManagementURL:   s.config.ManagementURL(),
		DNSVerified:     s.config.DNS.VerificationState == "verified",
		Request: certificate.SetupCertificateRequest{
			Domains: []string{strings.TrimSpace(s.config.Web.Hostname)}, CertificateType: "single",
			ProviderID: s.config.DNS.Provider, CredentialRef: s.config.DNS.CredentialRef,
			AccountEmail: s.config.ACME.AccountEmail, Environment: s.config.ACME.Environment,
			CertificateRoot: s.config.Paths.Certificate, BackupRoot: s.config.Paths.Backup,
		},
	}
}

// ActivateTLSCertificate 先持久化完整证书路径，再切换不可变快照；失败不影响旧连接。
// 此方法仅供进程内部续期流程调用，不提供上传或任意路径的 HTTP 接口。
func (s *Server) ActivateTLSCertificate(certificatePath, privateKeyPath string) error {
	return s.activateTLSCertificate(certificatePath, privateKeyPath, nil)
}

// ActivateManagementCertificate 拒绝覆盖签发期间发生的配置或证书变化。
func (s *Server) ActivateManagementCertificate(expected ManagementCertificateState, saved certificate.SavedResource) error {
	return s.activateTLSCertificate(saved.FullChainPath, saved.PrivateKeyPath, &expected)
}

func (s *Server) activateTLSCertificate(certificatePath, privateKeyPath string, expected *ManagementCertificateState) error {
	s.tlsMu.Lock()
	defer s.tlsMu.Unlock()
	return s.activateTLSCertificateLocked(certificatePath, privateKeyPath, expected, "")
}

// 调用者必须持有 tlsMu，保持双层协调与配置写入之间的独占边界。
func (s *Server) activateTLSCertificateLocked(certificatePath, privateKeyPath string, expected *ManagementCertificateState, expectedHash string) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if expected != nil {
		previous := *expected
		previous.NotAfter = time.Time{}
		if !reflect.DeepEqual(previous, s.managementCertificateState()) {
			return errors.New("管理证书签发期间配置已变化，已拒绝覆盖")
		}
	}
	if !s.config.SetupReady || strings.TrimSpace(s.configPath) == "" {
		return errors.New("管理证书热更新需要已初始化的持久化配置")
	}
	certificatePath = strings.TrimSpace(certificatePath)
	privateKeyPath = strings.TrimSpace(privateKeyPath)
	pair, err := tls.LoadX509KeyPair(certificatePath, privateKeyPath)
	if err != nil {
		return fmt.Errorf("加载新的管理证书失败：%w", err)
	}
	if expectedHash != "" && fmt.Sprintf("%x", sha256.Sum256(pair.Certificate[0])) != expectedHash {
		return errors.New("候选管理证书在协调期间发生变化，已拒绝切换")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("解析新的管理证书失败：%w", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return errors.New("新的管理证书不在有效期内")
	}
	if expected != nil && !leaf.NotAfter.After(expected.NotAfter) {
		return errors.New("新的管理证书未延长实际有效期")
	}
	host := strings.TrimSpace(s.config.Web.Hostname)
	if host == "" {
		host = strings.TrimSpace(s.config.Web.PublicIP)
	}
	if host == "" {
		return errors.New("管理域名或公网 IP 未配置")
	}
	// 校验与切换使用同一份内存数据，避免重复读文件造成校验对象变化。
	var chain []byte
	for _, der := range pair.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	validator := certificate.LocalValidator{VerifyChain: true, Roots: s.tlsRoots}
	if _, err := validator.ValidatePEM(context.Background(), chain, []string{host}); err != nil {
		return fmt.Errorf("新的管理证书校验失败：%w", err)
	}
	pair.Leaf = leaf
	candidate := s.config
	candidate.Server.TLSCert = certificatePath
	candidate.Server.TLSKey = privateKeyPath
	if err := candidate.Save(s.configPath); err != nil {
		return fmt.Errorf("保存管理证书配置失败：%w", err)
	}
	s.config = candidate
	s.tlsCertificate.Store(&pair)
	return nil
}

func (s *Server) getTLSCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	pair := s.tlsCertificate.Load()
	if pair == nil {
		return nil, errors.New("管理 HTTPS 证书尚未加载")
	}
	return pair, nil
}
