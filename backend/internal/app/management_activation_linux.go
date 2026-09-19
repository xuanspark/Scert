package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/httpapi"
	"git.pcux.cn/xuans/Scert/backend/internal/nginxactivation"
)

const managementActivationSocket = "/run/scert-nginx/activation.sock"

type coordinatedManagementActivator struct {
	server    *httpapi.Server
	directory string
	proxy     nginxactivation.ProxyClient
}

func newManagementActivator(cfg config.Config, server *httpapi.Server) (managementActivator, func(context.Context) error, error) {
	directory := filepath.Join(cfg.Paths.Data, "management-activation")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, nil, err
	}
	activator := coordinatedManagementActivator{
		server: server, directory: directory,
		proxy: nginxactivation.Client{SocketPath: managementActivationSocket, ServiceUID: 0},
	}
	return activator, activator.coordinator(nil).Recover, nil
}

func (a coordinatedManagementActivator) coordinator(expected *httpapi.ManagementCertificateState) nginxactivation.Coordinator {
	return nginxactivation.Coordinator{
		Directory: a.directory, Proxy: a.proxy,
		Upstream: managementUpstream{server: a.server, expected: expected},
	}
}

func (a coordinatedManagementActivator) Ready(ctx context.Context, state httpapi.ManagementCertificateState) error {
	if state.HTTPSMode == "direct" {
		if err := a.coordinator(nil).Recover(ctx); err != nil {
			return err
		}
		return (directManagementActivator{server: a.server}).Ready(ctx, state)
	}
	return a.coordinator(&state).Ready(ctx)
}

func (a coordinatedManagementActivator) Activate(ctx context.Context, state httpapi.ManagementCertificateState, saved certificate.SavedResource) error {
	if state.HTTPSMode == "direct" {
		if err := a.Ready(ctx, state); err != nil {
			return err
		}
		return (directManagementActivator{server: a.server}).Activate(ctx, state, saved)
	}
	return a.coordinator(&state).Activate(ctx, nginxactivation.CertificateFiles{
		CertificatePath: saved.FullChainPath, PrivateKeyPath: saved.PrivateKeyPath,
	})
}

type managementUpstream struct {
	server   *httpapi.Server
	expected *httpapi.ManagementCertificateState
}

func (u managementUpstream) WithCertificate(ctx context.Context, operation func(nginxactivation.ManagedSession) error) error {
	return u.server.WithManagementCertificate(ctx, func(session *httpapi.ManagementCertificateSession) error {
		return operation(&managementSession{session: session, expected: u.expected})
	})
}

type managementSession struct {
	session  *httpapi.ManagementCertificateSession
	expected *httpapi.ManagementCertificateState
	previous *httpapi.ManagementSnapshot
}

func (s *managementSession) Snapshot() (nginxactivation.ManagedCertificate, error) {
	current, err := s.session.Snapshot()
	if err != nil {
		return nginxactivation.ManagedCertificate{}, err
	}
	if s.previous == nil && s.expected != nil {
		expected, actual := *s.expected, current.State
		expected.NotAfter, actual.NotAfter = time.Time{}, time.Time{}
		if !reflect.DeepEqual(expected, actual) {
			return nginxactivation.ManagedCertificate{}, errors.New("签发期间管理配置已变化，拒绝开始双层激活")
		}
	}
	s.previous = &current
	return nginxactivation.ManagedCertificate{
		CertificateFiles: nginxactivation.CertificateFiles{CertificatePath: current.State.CertificatePath, PrivateKeyPath: current.State.PrivateKeyPath},
		Hostname:         current.State.Hostname, Mode: current.State.HTTPSMode, LeafHash: current.LeafHash,
		ProxyAddress: current.ProxyAddress, UpstreamAddress: current.UpstreamAddress,
	}, nil
}

func (s *managementSession) Activate(files nginxactivation.CertificateFiles, hash string) error {
	if s.previous == nil {
		return errors.New("管理证书协调缺少切换前快照")
	}
	return s.session.Activate(*s.previous, certificate.SavedResource{FullChainPath: files.CertificatePath, PrivateKeyPath: files.PrivateKeyPath}, hash)
}
