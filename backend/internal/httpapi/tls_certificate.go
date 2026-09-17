package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
)

type ManagementCertificateState struct {
	Hostname        string
	CertificatePath string
	PrivateKeyPath  string
	NotAfter        time.Time
}

// ManagementCertificate 返回实际配置的管理证书，不依赖业务域名仓储。
func (s *Server) ManagementCertificate() (ManagementCertificateState, error) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	state := ManagementCertificateState{
		Hostname:        strings.TrimSpace(s.config.Web.Hostname),
		CertificatePath: s.config.Server.TLSCert,
		PrivateKeyPath:  s.config.Server.TLSKey,
	}
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

// ActivateTLSCertificate 先持久化完整证书路径，再切换不可变快照；失败不影响旧连接。
// 此方法仅供进程内部续期流程调用，不提供上传或任意路径的 HTTP 接口。
func (s *Server) ActivateTLSCertificate(certificatePath, privateKeyPath string) error {
	s.tlsMu.Lock()
	defer s.tlsMu.Unlock()
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if !s.config.SetupReady || strings.TrimSpace(s.configPath) == "" {
		return errors.New("管理证书热更新需要已初始化的持久化配置")
	}
	certificatePath = strings.TrimSpace(certificatePath)
	privateKeyPath = strings.TrimSpace(privateKeyPath)
	pair, err := tls.LoadX509KeyPair(certificatePath, privateKeyPath)
	if err != nil {
		return fmt.Errorf("加载新的管理证书失败：%w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("解析新的管理证书失败：%w", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return errors.New("新的管理证书不在有效期内")
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
