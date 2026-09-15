package deployment

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// validateArtifact 是传输前的完整性检查；域名覆盖和环境信任策略由证书服务负责。
func validateArtifact(artifact Artifact) error {
	if len(artifact.Certificate) == 0 || len(artifact.FullChain) == 0 || len(artifact.PrivateKey) == 0 {
		return errors.New("证书部署文件不完整")
	}
	certificate, err := tls.X509KeyPair(artifact.Certificate, artifact.PrivateKey)
	if err != nil {
		return fmt.Errorf("部署证书与私钥不匹配：%w", err)
	}
	chain, err := tls.X509KeyPair(artifact.FullChain, artifact.PrivateKey)
	if err != nil {
		return fmt.Errorf("部署证书链与私钥不匹配：%w", err)
	}
	if !bytes.Equal(certificate.Certificate[0], chain.Certificate[0]) {
		return errors.New("部署证书与完整证书链的叶子证书不一致")
	}
	var previous *x509.Certificate
	remaining := bytes.TrimSpace(artifact.FullChain)
	now := time.Now()
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return errors.New("部署证书链包含无效 PEM 内容")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("解析部署证书链失败：%w", err)
		}
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return errors.New("部署证书链包含尚未生效或已过期的证书")
		}
		if previous != nil {
			if err := previous.CheckSignatureFrom(cert); err != nil {
				return fmt.Errorf("部署证书链签名或顺序无效：%w", err)
			}
		}
		previous = cert
		remaining = bytes.TrimSpace(rest)
	}
	return nil
}
