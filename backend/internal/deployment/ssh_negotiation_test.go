package deployment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHFactoryDefaultNegotiatesOnce(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "accepted"
		if reject {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			_, privateKey := newTestSSHSigner(t)
			hostSigner, _ := newTestSSHSigner(t)
			server := startTestSSHServer(t, func(ssh.PublicKey) error {
				if reject {
					return errors.ErrUnsupported
				}
				return nil
			}, hostSigner)
			factory := SSHFactory{
				Secrets: staticSecretReader{"secret://test": {"private_key": privateKey}},
				Timeout: 2 * time.Second,
			}
			client, err := factory.ConnectShell(t.Context(), testSSHServerModel(
				server, "secret://test", ssh.FingerprintSHA256(hostSigner.PublicKey()),
			))
			if client != nil {
				defer client.Close()
			}
			if (err != nil) != reject {
				t.Fatalf("reject=%t err=%v", reject, err)
			}
			if got := server.accepted.Load(); got != 1 {
				t.Fatalf("connections=%d; want=1", got)
			}
		})
	}
}

func TestSSHSingleNegotiationFallsBackToRegisteredFingerprint(t *testing.T) {
	clientSigner, _ := newTestSSHSigner(t)
	ecdsaHostSigner := newTestECDSAHostSigner(t)
	ed25519HostSigner, _ := newTestSSHSigner(t)
	var authenticated atomic.Int32
	server := startTestSSHServer(t, func(ssh.PublicKey) error {
		authenticated.Add(1)
		return nil
	}, ecdsaHostSigner, ed25519HostSigner)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	client, err := connectSSHClient(ctx,
		testSSHServerModel(server, "secret://test", ssh.FingerprintSHA256(ed25519HostSigner.PublicKey())),
		[]ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
		[]string{ssh.KeyAlgoECDSA256, ssh.KeyAlgoED25519}, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := server.accepted.Load(); got != 3 {
		t.Fatalf("connections=%d; want=3", got)
	}
	if got := authenticated.Load(); got != 1 {
		t.Fatalf("authenticated=%d; want=1", got)
	}
}

func TestSSHFactoryDefaultRejectsUnknownFingerprintBeforeAuthentication(t *testing.T) {
	_, privateKey := newTestSSHSigner(t)
	hostSigner, _ := newTestSSHSigner(t)
	var authenticated atomic.Int32
	server := startTestSSHServer(t, func(ssh.PublicKey) error {
		authenticated.Add(1)
		return nil
	}, hostSigner)
	factory := SSHFactory{
		Secrets: staticSecretReader{"secret://test": {"private_key": privateKey}},
		Timeout: 2 * time.Second,
	}
	client, err := factory.ConnectShell(t.Context(), testSSHServerModel(server, "secret://test", "SHA256:unregistered"))
	if client != nil {
		defer client.Close()
	}
	if err == nil {
		t.Fatal(err)
	}
	if got := authenticated.Load(); got != 0 {
		t.Fatalf("authenticated=%d; want=0", got)
	}
}
