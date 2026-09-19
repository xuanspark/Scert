package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// ManagementSnapshot 同时证明持久化路径、磁盘材料与当前 TLS 快照一致。
type ManagementSnapshot struct {
	State           ManagementCertificateState
	LeafHash        string
	UpstreamAddress string
	ProxyAddress    string
}

// ManagementCertificateSession 只在独占回调内有效，不持有配置读锁跨越网络探测。
type ManagementCertificateSession struct {
	mu     sync.Mutex
	server *Server
}

func (s *Server) WithManagementCertificate(ctx context.Context, operation func(*ManagementCertificateSession) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if operation == nil {
		return errors.New("管理证书操作不能为空")
	}
	if !s.tlsMu.TryLock() {
		return errors.New("管理证书或配置正在更新，请稍后重试")
	}
	defer s.tlsMu.Unlock()
	session := &ManagementCertificateSession{server: s}
	defer func() {
		session.mu.Lock()
		session.server = nil
		session.mu.Unlock()
	}()
	return operation(session)
}

func (session *ManagementCertificateSession) Snapshot() (ManagementSnapshot, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.server == nil {
		return ManagementSnapshot{}, errors.New("管理证书独占操作已经结束")
	}
	return session.server.managementSnapshotLocked()
}

func (session *ManagementCertificateSession) Activate(expected ManagementSnapshot, saved certificate.SavedResource, candidateHash string) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.server == nil {
		return errors.New("管理证书独占操作已经结束")
	}
	if candidateHash == "" {
		return errors.New("管理证书协调缺少候选摘要")
	}
	current, err := session.server.managementSnapshotLocked()
	if err != nil {
		return err
	}
	if current.LeafHash != expected.LeafHash || current.UpstreamAddress != expected.UpstreamAddress || current.ProxyAddress != expected.ProxyAddress {
		return errors.New("管理证书或监听配置在协调期间发生变化")
	}
	return session.server.activateTLSCertificateLocked(saved.FullChainPath, saved.PrivateKeyPath, &expected.State, candidateHash)
}

func (s *Server) managementSnapshotLocked() (ManagementSnapshot, error) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	var snapshot ManagementSnapshot
	if !s.config.SetupReady || s.configPath == "" {
		return snapshot, errors.New("管理证书协调需要已初始化的持久化配置")
	}
	persisted, err := config.Load(s.configPath)
	if err != nil {
		return snapshot, fmt.Errorf("读取管理证书持久化配置失败：%w", err)
	}
	state := s.managementCertificateState()
	if persisted.Server.TLSCert != state.CertificatePath || persisted.Server.TLSKey != state.PrivateKeyPath {
		return snapshot, errors.New("管理证书持久化路径与运行配置不一致，请先恢复服务")
	}
	pair, err := tls.LoadX509KeyPair(state.CertificatePath, state.PrivateKeyPath)
	if err != nil {
		return snapshot, fmt.Errorf("读取管理证书材料失败：%w", err)
	}
	live := s.tlsCertificate.Load()
	if live == nil || len(live.Certificate) == 0 || !bytes.Equal(live.Certificate[0], pair.Certificate[0]) {
		return snapshot, errors.New("管理证书磁盘材料与运行证书不一致，请先恢复服务")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return snapshot, err
	}
	address, err := listenAddress(s.config)
	if err != nil {
		return snapshot, err
	}
	port := s.config.Server.ProxyHTTPSPort
	if port == 0 {
		port = 443
	}
	state.NotAfter = leaf.NotAfter
	return ManagementSnapshot{
		State: state, LeafHash: fmt.Sprintf("%x", sha256.Sum256(leaf.Raw)),
		UpstreamAddress: address, ProxyAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	}, nil
}
