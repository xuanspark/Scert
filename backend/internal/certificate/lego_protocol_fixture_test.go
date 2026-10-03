package certificate

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/httpchallenge"
	"github.com/go-jose/go-jose/v4"
)

type protocolTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (p protocolTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == "https" && r.URL.Path == "/directory" &&
		(r.URL.Host == "acme-v02.api.letsencrypt.org" || r.URL.Host == "acme-staging-v02.api.letsencrypt.org") {
		r = r.Clone(r.Context())
		target := *p.target
		target.Path = "/directory"
		r.URL = &target
		r.Host = target.Host
	}
	if r.URL.Scheme != p.target.Scheme || r.URL.Host != p.target.Host {
		return nil, fmt.Errorf("隔离协议测试禁止访问外部地址")
	}
	return p.base.RoundTrip(r)
}

type protocolCA struct {
	mu         sync.Mutex
	server     *httptest.Server
	challenge  *httptest.Server
	client     *http.Client
	user       *AccountUser
	root       *x509.Certificate
	key        *ecdsa.PrivateKey
	names      []string
	statuses   []string
	nonces     map[string]bool
	sequence   int
	registered bool
	finalized  bool
	downloads  int
	proofs     int
	csrNames   []string
	chain      []byte
	reject     bool
	cancel     context.CancelFunc
	failures   []string
}

func newProtocolCA(t *testing.T, store *httpchallenge.Store, names []string) *protocolCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Scert protocol test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	c := &protocolCA{
		key: key, root: root, names: slices.Clone(names), nonces: make(map[string]bool),
		user:     &AccountUser{Email: "acme@example.com", PrivateKey: accountKey},
		statuses: make([]string, len(names)),
	}
	for i := range c.statuses {
		c.statuses[i] = "pending"
	}
	c.challenge = httptest.NewServer(store)
	t.Cleanup(c.challenge.Close)
	c.server = httptest.NewTLSServer(http.HandlerFunc(c.serveHTTP))
	t.Cleanup(c.server.Close)
	target, err := url.Parse(c.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// 仅注入测试 CA 信任，不跳过 TLS；真实签发器的生产/测试目录选择不变。
	c.client = c.server.Client()
	c.client.Timeout = 5 * time.Second
	c.client.Transport = protocolTransport{base: c.client.Transport, target: target}
	return c
}

func protocolToken(index int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("scert-isolated-acme-token-%d", index)))
}

func (c *protocolCA) fail(w http.ResponseWriter, err error) {
	c.failures = append(c.failures, err.Error())
	http.Error(w, "隔离 CA 协议断言失败", http.StatusBadRequest)
}

func (c *protocolCA) signedPayload(r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/jose+json" {
		return nil, fmt.Errorf("请求方法或签名媒体类型不正确")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	jws, err := jose.ParseSigned(string(data), []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(jws.Signatures) != 1 {
		return nil, fmt.Errorf("请求缺少唯一有效签名")
	}
	header := jws.Signatures[0].Protected
	if !c.nonces[header.Nonce] || header.ExtraHeaders[jose.HeaderKey("url")] != c.server.URL+r.URL.Path {
		return nil, fmt.Errorf("签名地址或一次性随机数无效")
	}
	delete(c.nonces, header.Nonce)
	if r.URL.Path == "/account" {
		if header.JSONWebKey == nil || header.KeyID != "" {
			return nil, fmt.Errorf("注册请求缺少账户公钥")
		}
	} else if !c.registered || header.KeyID != c.server.URL+"/account/1" || header.JSONWebKey != nil {
		return nil, fmt.Errorf("请求未引用已注册账户")
	}
	return jws.Verify(c.user.PrivateKey.Public())
}

func (c *protocolCA) order() map[string]any {
	status := "ready"
	ids := make([]map[string]string, len(c.names))
	auths := make([]string, len(c.names))
	for i, name := range c.names {
		ids[i] = map[string]string{"type": "dns", "value": name}
		auths[i] = fmt.Sprintf("%s/auth/%d", c.server.URL, i)
		if c.statuses[i] != "valid" {
			status = "pending"
		}
	}
	if c.finalized {
		status = "valid"
	}
	return map[string]any{"status": status, "identifiers": ids, "authorizations": auths,
		"finalize": c.server.URL + "/finalize", "certificate": c.server.URL + "/certificate"}
}

func (c *protocolCA) authorization(index int) map[string]any {
	challenge := map[string]any{
		"type": "http-01", "url": fmt.Sprintf("%s/challenge/%d", c.server.URL, index),
		"status": c.statuses[index], "token": protocolToken(index),
	}
	if c.statuses[index] == "invalid" {
		challenge["error"] = map[string]any{"type": "urn:ietf:params:acme:error:unauthorized", "detail": "隔离 CA 拒绝授权", "status": 403}
	}
	return map[string]any{
		"status": c.statuses[index], "identifier": map[string]string{"type": "dns", "value": c.names[index]},
		"challenges": []any{map[string]string{"type": "dns-01", "url": c.server.URL + "/forbidden-dns", "status": "pending", "token": protocolToken(index)}, challenge},
	}
}

func (c *protocolCA) readProof(ctx context.Context, name, token string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.challenge.URL+httpchallenge.PathPrefix+token, nil)
	if err != nil {
		return 0, "", err
	}
	req.Host = name
	resp, err := c.challenge.Client().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(data), err
}

func (c *protocolCA) serveHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	nonce := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("scert-isolated-nonce-%d", c.sequence)))
	c.nonces[nonce] = true
	w.Header().Set("Replay-Nonce", nonce)
	w.Header().Set("Content-Type", "application/json")
	write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	if r.URL.Path == "/directory" && r.Method == http.MethodGet {
		write(map[string]any{"newNonce": c.server.URL + "/nonce", "newAccount": c.server.URL + "/account", "newOrder": c.server.URL + "/order", "revokeCert": c.server.URL + "/revoke", "keyChange": c.server.URL + "/key-change"})
		return
	}
	if r.URL.Path == "/nonce" && r.Method == http.MethodHead {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	payload, err := c.signedPayload(r)
	if err != nil {
		c.fail(w, err)
		return
	}
	switch r.URL.Path {
	case "/account":
		var account struct {
			Terms   bool     `json:"termsOfServiceAgreed"`
			Contact []string `json:"contact"`
		}
		if json.Unmarshal(payload, &account) != nil || !account.Terms || !slices.Equal(account.Contact, []string{"mailto:" + c.user.Email}) {
			c.fail(w, fmt.Errorf("注册未确认条款或账户邮箱不符"))
			return
		}
		c.registered = true
		w.Header().Set("Location", c.server.URL+"/account/1")
		w.WriteHeader(http.StatusCreated)
		write(map[string]any{"status": "valid", "contact": account.Contact, "orders": c.server.URL + "/orders"})
	case "/order":
		var order struct {
			Identifiers []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"identifiers"`
		}
		if json.Unmarshal(payload, &order) != nil || len(order.Identifiers) != len(c.names) {
			c.fail(w, fmt.Errorf("订单域名数量不符"))
			return
		}
		for i, id := range order.Identifiers {
			if id.Type != "dns" || id.Value != c.names[i] {
				c.fail(w, fmt.Errorf("订单域名被修改"))
				return
			}
		}
		w.Header().Set("Location", c.server.URL+"/order/1")
		w.WriteHeader(http.StatusCreated)
		write(c.order())
	case "/order/1":
		if len(payload) != 0 {
			c.fail(w, fmt.Errorf("订单查询不是签名空载荷"))
			return
		}
		write(c.order())
	case "/finalize":
		if err := c.finalize(payload); err != nil {
			c.fail(w, err)
			return
		}
		write(c.order())
	case "/certificate":
		if !c.finalized || len(payload) != 0 {
			c.fail(w, fmt.Errorf("证书下载早于订单完成或载荷非空"))
			return
		}
		c.downloads++
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		_, _ = w.Write(c.chain)
	default:
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 2 || (parts[0] != "auth" && parts[0] != "challenge") {
			c.fail(w, fmt.Errorf("客户端访问了未授权协议路径：%s", r.URL.Path))
			return
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil || index < 0 || index >= len(c.names) {
			c.fail(w, fmt.Errorf("授权索引无效"))
			return
		}
		if parts[0] == "auth" {
			if len(payload) != 0 {
				var state struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(payload, &state) != nil || state.Status != "deactivated" {
					c.fail(w, fmt.Errorf("授权更新不是合法停用"))
					return
				}
				c.statuses[index] = "deactivated"
			}
			write(c.authorization(index))
			return
		}
		if err := c.verifyChallenge(r.Context(), index); err != nil {
			c.fail(w, err)
			return
		}
		write(c.authorization(index)["challenges"].([]any)[1])
	}
}

func (c *protocolCA) verifyChallenge(ctx context.Context, index int) error {
	key := jose.JSONWebKey{Key: c.user.PrivateKey.Public()}
	thumbprint, err := key.Thumbprint(crypto.SHA256)
	if err != nil {
		return err
	}
	code, value, err := c.readProof(ctx, c.names[index], protocolToken(index))
	if err != nil || code != http.StatusOK || value != protocolToken(index)+"."+base64.RawURLEncoding.EncodeToString(thumbprint) {
		return fmt.Errorf("真实 HTTP 挑战未返回账户公钥绑定证明：状态=%d", code)
	}
	c.proofs++
	c.statuses[index] = "valid"
	if c.reject {
		c.statuses[index] = "invalid"
	}
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

func (c *protocolCA) finalize(payload []byte) error {
	for _, status := range c.statuses {
		if status != "valid" {
			return fmt.Errorf("未完成全部授权就提交 CSR")
		}
	}
	var request struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return err
	}
	der, err := base64.RawURLEncoding.DecodeString(request.CSR)
	if err != nil {
		return err
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return err
	}
	if err := csr.CheckSignature(); err != nil {
		return err
	}
	if !slices.Equal(csr.DNSNames, c.names) {
		return fmt.Errorf("CSR 的 SAN 与原订单不符")
	}
	c.csrNames = slices.Clone(csr.DNSNames)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: csr.Subject, DNSNames: csr.DNSNames,
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * time.Minute),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err = x509.CreateCertificate(rand.Reader, leaf, c.root, csr.PublicKey, c.key)
	if err != nil {
		return err
	}
	c.chain = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	c.chain = append(c.chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.root.Raw})...)
	c.finalized = true
	return nil
}
