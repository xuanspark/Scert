package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

func TestServiceCreatesListsValidatesAndDeletesSSHKey(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	service := NewService(store)
	privateKey := testPrivateKey(t)
	created, err := service.Create(t.Context(), "生产部署私钥", privateKey, "")
	if err != nil {
		t.Fatalf("创建 SSH 私钥失败：%v", err)
	}
	if created.ID == "" || !strings.HasPrefix(created.Reference, ReferencePrefix) || created.Algorithm != "ssh-ed25519" || !strings.HasPrefix(created.Fingerprint, "SHA256:") {
		t.Fatalf("SSH 私钥元数据无效：%+v", created)
	}
	items, err := service.List(t.Context())
	if err != nil || len(items) != 1 || items[0].Name != "生产部署私钥" {
		t.Fatalf("SSH 私钥列表无效：items=%+v err=%v", items, err)
	}
	validated, err := service.ValidateReference(t.Context(), created.Reference)
	if err != nil || validated.Fingerprint != created.Fingerprint {
		t.Fatalf("SSH 私钥引用校验失败：metadata=%+v err=%v", validated, err)
	}
	if err := service.Delete(t.Context(), created.ID); err != nil {
		t.Fatalf("删除 SSH 私钥失败：%v", err)
	}
	if _, err := service.ValidateReference(t.Context(), created.Reference); err == nil {
		t.Fatal("已删除的 SSH 私钥引用不应继续通过校验")
	}
}

func TestServiceRejectsInvalidSSHKeyWithoutLeakingContent(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	privateKey := "very-sensitive-invalid-private-key"
	_, err = NewService(store).Create(t.Context(), "错误私钥", privateKey, "")
	if err == nil {
		t.Fatal("无效 SSH 私钥应被拒绝")
	}
	if strings.Contains(err.Error(), privateKey) {
		t.Fatalf("错误信息不应泄露私钥内容：%v", err)
	}
}

func TestServiceSupportsEncryptedSSHKeyAndRejectsWrongPassphrase(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("failed to open secret store: %v", err)
	}
	privateKey := testEncryptedPrivateKey(t, "correct-passphrase")
	service := NewService(store)
	created, err := service.Create(t.Context(), "encrypted deployment key", privateKey, "correct-passphrase")
	if err != nil {
		t.Fatalf("failed to create encrypted SSH key: %v", err)
	}
	if created.Algorithm != "ssh-ed25519" || !strings.HasPrefix(created.Fingerprint, "SHA256:") {
		t.Fatalf("invalid encrypted SSH key metadata: %+v", created)
	}
	_, err = service.Create(t.Context(), "wrong passphrase key", privateKey, "wrong-passphrase")
	if err == nil || strings.Contains(err.Error(), "wrong-passphrase") || strings.Contains(err.Error(), "correct-passphrase") {
		t.Fatalf("wrong passphrase was not rejected safely: %v", err)
	}
}

func TestServiceRejectsOversizedPrivateKeyAndPassphrase(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("failed to open secret store: %v", err)
	}
	service := NewService(store)
	if _, err := service.Create(t.Context(), "oversized key", strings.Repeat("a", MaxPrivateKeyBytes+1), ""); err == nil {
		t.Fatal("oversized SSH private key should be rejected")
	}
	if _, err := service.Create(t.Context(), "oversized passphrase", testPrivateKey(t), strings.Repeat("p", maxPassphraseBytes+1)); err == nil {
		t.Fatal("oversized SSH private key passphrase should be rejected")
	}
}

func testPrivateKey(t *testing.T) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成测试私钥失败：%v", err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("编码测试私钥失败：%v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

func testEncryptedPrivateKey(t *testing.T, passphrase string) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "scert-test", []byte(passphrase))
	if err != nil {
		t.Fatalf("failed to encrypt test private key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}
