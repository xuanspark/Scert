package notification

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Send 通过所有已启用的出站通道发送通知。
func (s *Service) Send(ctx context.Context, event Event) error {
	if s == nil {
		return errors.New("通知服务未初始化")
	}
	if !s.config.HTTPEnabled && !s.config.SMTPEnabled {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.config.UsesSmailSystemAccount() {
		if !s.config.SMTPEnabled {
			return errors.New("Smail 系统通知账号必须启用 SMTP 通道")
		}
		payload, err := s.buildPayload(event)
		if err != nil {
			return err
		}
		return s.sendSMTPWithRetry(ctx, payload)
	}
	var errs []error
	if s.config.HTTPEnabled {
		if err := s.sendHTTP(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	if s.config.SMTPEnabled {
		payload, err := s.buildPayload(event)
		if err != nil {
			errs = append(errs, err)
		} else if err := s.sendSMTPWithRetry(ctx, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) sendSMTPWithRetry(ctx context.Context, payload mailPayload) error {
	if strings.TrimSpace(s.config.SMTPHost) == "" {
		return errors.New("SMTP 主机不能为空")
	}
	credentials, err := s.loadSMTPCredentials(ctx)
	if err != nil {
		return err
	}
	var lastErr error
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
		if err := s.sendSMTPOnce(ctx, payload, credentials); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return fmt.Errorf("发送 SMTP 通知失败，已重试 %d 次：%w", s.config.RetryCount, lastErr)
}

func (s *Service) loadSMTPCredentials(ctx context.Context) (map[string]string, error) {
	ref := strings.TrimSpace(s.config.SMTPCredentialRef)
	if ref == "" {
		return nil, nil
	}
	if s.credentials == nil {
		return nil, errors.New("SMTP 通知凭据存储未配置")
	}
	values, err := s.credentials.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("读取 SMTP 通知凭据失败：%w", err)
	}
	return values, nil
}

func (s *Service) sendSMTPOnce(ctx context.Context, payload mailPayload, credentials map[string]string) error {
	host := strings.TrimSpace(s.config.SMTPHost)
	if host == "" {
		return errors.New("SMTP 主机不能为空")
	}
	timeout := time.Duration(s.config.TimeoutSecs) * time.Second
	if timeout < time.Second {
		timeout = time.Second
	}
	address := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(s.config.SMTPPort))
	conn, err := s.dialSMTP(ctx, address, host, s.config.SMTPMode, timeout)
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败：%w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("初始化 SMTP 客户端失败：%w", err)
	}
	defer client.Close()
	if s.config.SMTPMode == "starttls" {
		tlsConfig := s.smtpTLSConfig(host)
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("建立 SMTP STARTTLS 连接失败：%w", err)
		}
	}
	username := strings.TrimSpace(s.config.SMTPUsername)
	if username == "" {
		username = credentialValue(credentials, "username", "account", "email")
	}
	password := credentialValue(credentials, "password", "pass")
	if username != "" {
		if password == "" {
			return errors.New("SMTP 凭据中缺少密码")
		}
		if s.config.SMTPMode == "plain" && !s.config.SMTPAllowPlaintextAuth {
			return errors.New("SMTP 明文连接禁止认证，请启用 TLS 或明确允许明文认证")
		}
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return fmt.Errorf("SMTP 认证失败：%w", err)
		}
	}
	recipients := make([]string, 0, len(payload.To))
	for _, item := range payload.To {
		recipients = append(recipients, item.Email)
	}
	if err := client.Mail(payload.From.Email); err != nil {
		return fmt.Errorf("设置 SMTP 发件人失败：%w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("设置 SMTP 收件人失败：%w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("打开 SMTP 邮件数据通道失败：%w", err)
	}
	if _, err := writer.Write(buildSMTPMessage(payload)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("写入 SMTP 邮件内容失败：%w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("提交 SMTP 邮件失败：%w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("关闭 SMTP 会话失败：%w", err)
	}
	return nil
}

func (s *Service) dialSMTP(ctx context.Context, address, host, mode string, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if mode != "implicit_tls" {
		return conn, nil
	}
	tlsConfig := s.smtpTLSConfig
	if tlsConfig == nil {
		tlsConfig = func(serverName string) *tls.Config {
			return &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
		}
	}
	tlsConn := tls.Client(conn, tlsConfig(host))
	if err := tlsConn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return tlsConn, nil
}

func buildSMTPMessage(payload mailPayload) []byte {
	from := formatSMTPAddress(payload.From)
	to := make([]string, 0, len(payload.To))
	for _, item := range payload.To {
		to = append(to, formatSMTPAddress(item))
	}
	subject := mime.QEncoding.Encode("UTF-8", payload.Subject)
	content := payload.Text
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.ReplaceAll(content, "\n", "\r\n")
	if !strings.HasSuffix(content, "\r\n") {
		content += "\r\n"
	}
	message := "From: " + from + "\r\n" +
		"To: " + strings.Join(to, ", ") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" + content
	return []byte(message)
}

func formatSMTPAddress(value address) string {
	parsed, err := mail.ParseAddress(value.Email)
	if err != nil || parsed.Address == "" {
		return value.Email
	}
	name := strings.TrimSpace(value.Name)
	if name == "" {
		name = parsed.Name
	}
	if name == "" {
		return parsed.Address
	}
	return mime.QEncoding.Encode("UTF-8", name) + " <" + parsed.Address + ">"
}
