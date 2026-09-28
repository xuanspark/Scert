package deployment

import (
	"errors"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

func TestSharedServerDomainPaths(t *testing.T) {
	domains := []domain.Domain{
		{ID: "a", Name: "a.example.test", CertificateType: domain.CertificateSingle},
		{ID: "b", Name: "b.example.test", CertificateType: domain.CertificateSingle},
	}
	for _, tc := range []struct {
		name     string
		server   Server
		conflict bool
	}{
		{name: "default_templates"},
		{name: "english_templates", server: Server{CertificatePath: "/ssl/{domain}/cert.pem", CertificateChainPath: "/ssl/{domain}/chain.pem", PrivateKeyPath: "/ssl/{domain}/key.pem"}},
		{name: "fixed_certificate", server: Server{CertificatePath: "/ssl/cert.pem"}, conflict: true},
		{name: "fixed_chain", server: Server{CertificateChainPath: "/ssl/chain.pem"}, conflict: true},
		{name: "fixed_key", server: Server{PrivateKeyPath: "/ssl/key.pem"}, conflict: true},
		{name: "collapsed_placeholder", server: Server{CertificatePath: "/ssl/{domain}/../cert.pem"}, conflict: true},
		{name: "cross_file_collision", server: Server{CertificatePath: "/ssl/{domain}/b.example.test", PrivateKeyPath: "/ssl/a.example.test/{domain}"}, conflict: true},
		{name: "file_directory_collision", server: Server{CertificatePath: "/ssl/{domain}/b.example.test", PrivateKeyPath: "/ssl/a.example.test/{domain}/key.pem"}, conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateServerDomainPaths(tc.server, domains)
			if tc.conflict {
				if !errors.Is(err, ErrSharedPathConflict) {
					t.Fatalf("共享路径冲突未拒绝：%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := ValidateServerDomainPaths(Server{CertificatePath: "/ssl/cert.pem"}, domains[:1]); err != nil {
		t.Fatalf("单域名固定路径应保持兼容：%v", err)
	}
	for _, item := range []domain.Domain{
		{ID: "other", Name: "a.example.test", CertificateType: domain.CertificateSingle},
		{ID: "wildcard", Name: "*.a.example.test", CertificateType: domain.CertificateSingle},
	} {
		if err := ValidateServerDomainPaths(Server{}, []domain.Domain{domains[0], item}); !errors.Is(err, ErrSharedPathConflict) {
			t.Fatalf("规范化后同路径的资源未拒绝：%v", err)
		}
	}
}
