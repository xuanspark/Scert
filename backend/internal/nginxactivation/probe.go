package nginxactivation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// Endpoint 只允许探测本机指定端口，域名仍用于 SNI、Host 和可信链校验。
type Endpoint struct {
	Address  string
	Hostname string
	Roots    *x509.CertPool
}

func (e Endpoint) validate() error {
	host, port, err := net.SplitHostPort(e.Address)
	number, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || portErr != nil || number < 1 || number > 65535 || ip == nil || !ip.IsLoopback() {
		return errors.New("管理证书探测地址必须是本机回环 IP 和有效端口")
	}
	if !domain.IsValidName(e.Hostname) || net.ParseIP(e.Hostname) != nil {
		return errors.New("管理证书探测域名无效")
	}
	return nil
}

// Check 每次建立新 TLS 连接，不复用连接、不使用系统代理、不跟随跳转。
// expected 为空时读取已验证的当前证书，否则同时校验实际叶证书字节。
func (e Endpoint) Check(ctx context.Context, expected []byte) ([]byte, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, port, _ := net.SplitHostPort(e.Address)
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: e.Hostname, RootCAs: e.Roots},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", e.Address)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	address := url.URL{Scheme: "https", Host: net.JoinHostPort(e.Hostname, port), Path: "/readyz"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("管理入口 TLS 探测失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || len(response.TLS.PeerCertificates) == 0 {
		return nil, errors.New("管理入口未通过 HTTPS 就绪检查")
	}
	actual := response.TLS.PeerCertificates[0].Raw
	if len(expected) != 0 && !bytes.Equal(expected, actual) {
		return nil, errors.New("管理入口仍未提供预期的新证书")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(body) > 4096 {
		return nil, errors.New("管理入口就绪响应读取失败或超过限制")
	}
	var readiness struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(body, &readiness); err != nil || readiness.Service != "scert" || readiness.Status != "ready" {
		return nil, errors.New("管理入口未返回 Scert 就绪状态")
	}
	return bytes.Clone(actual), nil
}

func (e Endpoint) Wait(ctx context.Context, expected []byte) error {
	if len(expected) == 0 {
		return errors.New("等待管理入口切换时必须指定证书")
	}
	if err := e.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		_, err := e.Check(ctx, expected)
		if err == nil {
			return nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), err)
		case <-timer.C:
		}
	}
}
