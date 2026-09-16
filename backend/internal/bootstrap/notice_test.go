package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestBootstrapNoticeMatchesTLSCertificateFingerprint(t *testing.T) {
	material, err := NewTemporaryTLSMaterial("203.0.113.20")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writeBootstrapNotice(&output, 8080, "/run/scert/bootstrap-url", material)
	digest := sha256.Sum256(material.Leaf.Raw)
	want := strings.ToUpper(hex.EncodeToString(digest[:]))
	if !strings.Contains(output.String(), "SHA-256 指纹："+want) || !strings.Contains(output.String(), "/run/scert/bootstrap-url") {
		t.Fatal("启动提示缺少实际 TLS 证书的 SHA-256 指纹或受保护地址路径")
	}
	for _, forbidden := range []string{"token=", "PRIVATE KEY", "CERTIFICATE-----"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatal("启动提示不得泄露令牌、私钥或完整证书材料")
		}
	}
}
