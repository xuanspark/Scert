package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

// CredentialReader 读取加密凭据存储中的通知认证信息。
type CredentialReader interface {
	Get(context.Context, string) (map[string]string, error)
}

// HTTPDoer 抽象 HTTP 客户端，便于测试重试、认证和响应处理。
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Notifier 发送证书任务通知。
type Notifier interface {
	Send(context.Context, Event) error
}

// Event 描述一次证书申请、续期、导入或部署任务。
type Event struct {
	Type              string
	Status            string
	DomainID          string
	Domain            string
	Domains           []string
	ExpiresAt         *time.Time
	CertificateSerial string
	Deployment        deployment.Report
	ManagementURL     string
	Error             string
}

type address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

type mailPayload struct {
	From    address   `json:"from"`
	To      []address `json:"to"`
	Subject string    `json:"subject"`
	Text    string    `json:"text"`
	HTML    string    `json:"html"`
	CC      []address `json:"cc"`
	BCC     []address `json:"bcc"`
	Attrs   []any     `json:"attrs"`
}

type sessionAuth struct {
	cookie string
	csrf   string
	origin string
}

type sessionExpiredError struct {
	statusCode int
	errorCode  int
}

func (e *sessionExpiredError) Error() string {
	if e.statusCode != 0 {
		return fmt.Sprintf("邮件服务登录会话已失效，HTTP 状态码 %d", e.statusCode)
	}
	return fmt.Sprintf("邮件服务登录会话已失效，业务错误码 %d", e.errorCode)
}

// Service 通过 HTTP 邮件 API 发送通知。
type Service struct {
	config        config.NotificationConfig
	credentials   CredentialReader
	client        HTTPDoer
	now           func() time.Time
	requestID     func() string
	sleep         func(context.Context, time.Duration) error
	smtpTLSConfig func(string) *tls.Config
}

// New 创建 HTTP 通知服务。SMTP 配置暂保留为兼容字段，当前发送器只执行 HTTP 通知。
func New(cfg config.NotificationConfig, credentials CredentialReader) *Service {
	service := &Service{
		config:      cfg,
		credentials: credentials,
		client:      &http.Client{},
		now:         time.Now,
		requestID:   newRequestID,
		sleep:       sleepContext,
	}
	service.smtpTLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	return service
}

// Send 按配置发送通知；未启用任何通知通道时直接返回成功。
func (s *Service) sendHTTP(ctx context.Context, event Event) error {
	if s == nil {
		return errors.New("通知服务未初始化")
	}
	if !s.config.HTTPEnabled {
		return nil
	}
	if s.config.UsesSmailSystemAccount() {
		return errors.New("Smail 系统通知账号只能通过 SMTP 通道发送")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := s.buildPayload(event)
	if err != nil {
		return err
	}
	return s.sendHTTPPayload(ctx, payload)
}

func (s *Service) sendHTTPPayload(ctx context.Context, payload mailPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码邮件通知失败：%w", err)
	}
	endpoint, err := notificationURL(s.config.Endpoint, s.config.HTTPPath)
	if err != nil {
		return err
	}
	credentials, err := s.loadCredentials(ctx)
	if err != nil {
		return err
	}
	var session sessionAuth
	if s.config.HTTPAuthMode == "session" {
		session, err = s.loginSession(ctx, credentials)
		if err != nil {
			return err
		}
	}
	requestID := s.requestID()
	if requestID == "" {
		return errors.New("生成通知请求 ID 失败")
	}
	var lastErr error
	refreshSession := false
	for attempt := 0; attempt <= s.config.RetryCount; attempt++ {
		if attempt > 0 {
			interval := time.Duration(0)
			if attempt-1 < len(s.config.RetryIntervalsSecs) {
				interval = time.Duration(s.config.RetryIntervalsSecs[attempt-1]) * time.Second
			}
			if err := s.sleep(ctx, interval); err != nil {
				return err
			}
		}
		if attempt > 0 && s.config.HTTPAuthMode == "session" && refreshSession {
			session, err = s.loginSession(ctx, credentials)
			if err != nil {
				lastErr = err
				continue
			}
			refreshSession = false
		}
		err := s.sendOnce(ctx, endpoint, body, credentials, requestID, session)
		if err == nil {
			return nil
		}
		lastErr = err
		var expired *sessionExpiredError
		refreshSession = errors.As(err, &expired)
	}
	return fmt.Errorf("发送邮件通知失败，已重试 %d 次：%w", s.config.RetryCount, lastErr)
}

func (s *Service) sendOnce(ctx context.Context, endpoint string, body []byte, credentials map[string]string, requestID string, session sessionAuth) error {
	timeout := time.Duration(s.config.TimeoutSecs) * time.Second
	if timeout < time.Second {
		timeout = time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建邮件通知请求失败：%w", err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Scert-Request-Id", requestID)
	timestamp := strconv.FormatInt(s.now().UTC().Unix(), 10)
	request.Header.Set("X-Scert-Timestamp", timestamp)
	if s.config.HTTPAuthMode == "session" {
		if session.cookie == "" {
			return errors.New("HTTP 通知登录会话为空")
		}
		request.Header.Set("Cookie", session.cookie)
		if session.csrf != "" {
			request.Header.Set("X-CSRF-Token", session.csrf)
		}
		request.Header.Set("Origin", session.origin)
		request.Header.Set("Referer", session.origin+"/")
	}
	for key, value := range s.config.HTTPHeaders {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("HTTP 自定义请求头无效：%s", key)
		}
		if isProtectedHeader(key) {
			return fmt.Errorf("HTTP 自定义请求头不允许覆盖安全头：%s", key)
		}
		request.Header.Set(key, value)
	}
	switch s.config.HTTPAuthMode {
	case "none", "":
	case "bearer":
		token := credentialValue(credentials, "token", "access_token")
		if token == "" {
			return errors.New("HTTP Bearer 凭据中缺少 Token")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	case "hmac_sha256":
		secret := credentialValue(credentials, "secret", "hmac_secret", "key", "token")
		if secret == "" {
			return errors.New("HTTP HMAC 凭据中缺少签名密钥")
		}
		signature := sign(secret, timestamp, requestID, body)
		request.Header.Set("X-Scert-Signature", "sha256="+signature)
	case "session":
	default:
		return fmt.Errorf("HTTP 通知认证方式无效：%s", s.config.HTTPAuthMode)
	}
	client := s.client
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求邮件服务失败：%w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return fmt.Errorf("读取邮件服务响应失败：%w", err)
	}
	if len(responseBody) > 64<<10 {
		return errors.New("邮件服务响应超过允许大小")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return &sessionExpiredError{statusCode: response.StatusCode}
		}
		return fmt.Errorf("邮件服务返回 HTTP %d", response.StatusCode)
	}
	var envelope struct {
		ErrorNo *int `json:"errorNo"`
	}
	if len(responseBody) > 0 && json.Unmarshal(responseBody, &envelope) == nil && envelope.ErrorNo != nil && *envelope.ErrorNo != 0 {
		if *envelope.ErrorNo == http.StatusUnauthorized || *envelope.ErrorNo == http.StatusForbidden {
			return &sessionExpiredError{errorCode: *envelope.ErrorNo}
		}
		return fmt.Errorf("邮件服务业务失败，错误码 %d", *envelope.ErrorNo)
	}
	return nil
}

func (s *Service) loginSession(ctx context.Context, credentials map[string]string) (sessionAuth, error) {
	account := credentialValue(credentials, "account", "username", "identifier")
	password := credentials["password"]
	if account == "" || password == "" {
		return sessionAuth{}, errors.New("HTTP 通知会话凭据必须包含账号和密码")
	}
	loginURL, err := notificationURL(s.config.Endpoint, s.config.HTTPLoginPath)
	if err != nil {
		return sessionAuth{}, err
	}
	payload, err := json.Marshal(map[string]string{"account": account, "password": password})
	if err != nil {
		return sessionAuth{}, fmt.Errorf("编码 HTTP 通知登录请求失败：%w", err)
	}
	timeout := time.Duration(s.config.TimeoutSecs) * time.Second
	if timeout < time.Second {
		timeout = time.Second
	}
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(loginCtx, http.MethodPost, loginURL, bytes.NewReader(payload))
	if err != nil {
		return sessionAuth{}, fmt.Errorf("创建 HTTP 通知登录请求失败：%w", err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", notificationOrigin(loginURL))
	request.Header.Set("Referer", notificationOrigin(loginURL)+"/")
	client := s.client
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(request)
	if err != nil {
		return sessionAuth{}, fmt.Errorf("请求 HTTP 通知登录接口失败：%w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return sessionAuth{}, fmt.Errorf("读取 HTTP 通知登录响应失败：%w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return sessionAuth{}, fmt.Errorf("HTTP 通知登录接口返回 HTTP %d", response.StatusCode)
	}
	var envelope struct {
		ErrorNo  *int   `json:"errorNo"`
		ErrorMsg string `json:"errorMsg"`
	}
	if len(responseBody) > 0 && json.Unmarshal(responseBody, &envelope) == nil && envelope.ErrorNo != nil && *envelope.ErrorNo != 0 {
		if strings.TrimSpace(envelope.ErrorMsg) == "" {
			return sessionAuth{}, fmt.Errorf("HTTP 通知登录失败，错误码 %d", *envelope.ErrorNo)
		}
		return sessionAuth{}, fmt.Errorf("HTTP 通知登录失败，错误码 %d：%s", *envelope.ErrorNo, strings.TrimSpace(envelope.ErrorMsg))
	}
	cookies := make([]string, 0, len(response.Cookies()))
	csrf := ""
	for _, cookie := range response.Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
		if cookie.Name == "smail_csrf" {
			csrf = cookie.Value
		}
	}
	if len(cookies) == 0 {
		return sessionAuth{}, errors.New("HTTP 通知登录成功但未返回会话 Cookie")
	}
	return sessionAuth{cookie: strings.Join(cookies, "; "), csrf: csrf, origin: notificationOrigin(loginURL)}, nil
}

func notificationOrigin(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

func (s *Service) loadCredentials(ctx context.Context) (map[string]string, error) {
	if s.config.HTTPAuthMode == "none" || s.config.HTTPAuthMode == "" {
		return nil, nil
	}
	if s.credentials == nil {
		return nil, errors.New("HTTP 通知凭据存储未配置")
	}
	ref := strings.TrimSpace(s.config.HTTPCredentialRef)
	if ref == "" {
		return nil, errors.New("HTTP 通知凭据引用不能为空")
	}
	values, err := s.credentials.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("读取 HTTP 通知凭据失败：%w", err)
	}
	return values, nil
}

func (s *Service) buildPayload(event Event) (mailPayload, error) {
	fromName, fromEmail, err := s.config.SenderIdentity()
	if err != nil {
		return mailPayload{}, err
	}
	if fromEmail == "" {
		return mailPayload{}, errors.New("通知发件人地址不能为空")
	}
	parsedSender, err := mail.ParseAddress(fromEmail)
	if err != nil || parsedSender.Address != fromEmail {
		return mailPayload{}, errors.New("通知发件人地址格式无效")
	}
	recipients := make([]address, 0, len(s.config.Recipients))
	for _, value := range s.config.Recipients {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Address != value {
			return mailPayload{}, fmt.Errorf("通知收件人地址格式无效：%s", value)
		}
		recipients = append(recipients, address{Name: parsed.Name, Email: parsed.Address})
	}
	if len(recipients) == 0 {
		return mailPayload{}, errors.New("通知收件人不能为空")
	}
	status := eventStatus(event.Status)
	domainName := strings.TrimSpace(event.Domain)
	if domainName == "" {
		for _, domain := range event.Domains {
			if domainName = strings.TrimSpace(domain); domainName != "" {
				break
			}
		}
	}
	if domainName == "" {
		domainName = "未指定域名"
	}
	title := "证书任务"
	if s.config.EventMode == "unified" {
		title = "证书任务通知"
	} else if event.Type != "" {
		title = eventTypeName(event.Type)
	}
	text, htmlBody := buildMailContent(event, title, status, domainName, s.config)
	return mailPayload{
		From:    address{Name: fromName, Email: fromEmail},
		To:      recipients,
		Subject: buildMailSubject(title, status, domainName),
		Text:    text,
		HTML:    htmlBody,
		CC:      []address{},
		BCC:     []address{},
		Attrs:   []any{},
	}, nil
}

func eventStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success":
		return "成功"
	case "failed", "failure":
		return "失败"
	case "error":
		return "错误"
	default:
		return "未知"
	}
}

func eventTypeName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "certificate_issue":
		return "证书申请"
	case "certificate_renewal":
		return "证书续期"
	case "management_renewal":
		return "管理证书续期"
	case "certificate_import":
		return "证书导入"
	default:
		return "证书任务"
	}
}

func notificationURL(endpoint, path string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", errors.New("第三方邮箱 API 地址格式无效")
	}
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", errors.New("HTTP 通知请求路径必须以 / 开头")
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

func isProtectedHeader(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "authorization", "cookie", "origin", "referer", "content-type", "accept", "x-csrf-token", "x-scert-signature", "x-scert-request-id", "x-scert-timestamp":
		return true
	default:
		return false
	}
}

func credentialValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func sign(secret, timestamp, requestID string, body []byte) string {
	hasher := hmac.New(sha256.New, []byte(secret))
	_, _ = hasher.Write([]byte(timestamp + "." + requestID + "."))
	_, _ = hasher.Write(body)
	return hex.EncodeToString(hasher.Sum(nil))
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(value[:])
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
