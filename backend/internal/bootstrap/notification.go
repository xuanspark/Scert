package bootstrap

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

// NotificationInput 不接受客户端指定凭据引用；新密码只写入加密存储。
// 省略整个通知对象时保留原配置，提交对象时按完整表单应用配置。
type NotificationInput struct {
	HTTPEnabled              bool              `json:"http_enabled"`
	SMTPEnabled              bool              `json:"smtp_enabled"`
	Provider                 string            `json:"provider"`
	Endpoint                 string            `json:"endpoint"`
	HTTPPath                 string            `json:"http_path"`
	HTTPLoginPath            string            `json:"http_login_path"`
	HTTPAuthMode             string            `json:"http_auth_mode"`
	HTTPCredentials          map[string]string `json:"http_credentials"`
	HTTPHeaders              map[string]string `json:"http_headers"`
	EventMode                string            `json:"event_mode"`
	IncludeDeploymentDetails bool              `json:"include_deployment_details"`
	IncludeCertificateSerial bool              `json:"include_certificate_serial"`
	IncludeManagementURL     bool              `json:"include_management_url"`
	SenderName               string            `json:"sender_name"`
	SenderEmail              string            `json:"sender_email"`
	Recipients               []string          `json:"recipients"`
	TimeoutSecs              int               `json:"timeout_secs"`
	RetryCount               int               `json:"retry_count"`
	RetryIntervalsSecs       []int             `json:"retry_intervals_secs"`
	SMTPHost                 string            `json:"smtp_host"`
	SMTPPort                 int               `json:"smtp_port"`
	SMTPUsername             string            `json:"smtp_username"`
	SMTPPassword             string            `json:"smtp_password"`
	SMTPMode                 string            `json:"smtp_mode"`
	SMTPAllowPlaintextAuth   bool              `json:"smtp_allow_plaintext_auth"`
}

func (completer *Completer) prepareNotification(ctx context.Context, current config.NotificationConfig, input *NotificationInput) (config.NotificationConfig, func() error, error) {
	if input == nil {
		return current, nil, nil
	}
	candidate := config.NotificationConfig{
		HTTPEnabled: input.HTTPEnabled, SMTPEnabled: input.SMTPEnabled,
		Provider: strings.TrimSpace(input.Provider), Endpoint: strings.TrimSpace(input.Endpoint),
		HTTPPath: strings.TrimSpace(input.HTTPPath), HTTPLoginPath: strings.TrimSpace(input.HTTPLoginPath),
		HTTPAuthMode: strings.ToLower(strings.TrimSpace(input.HTTPAuthMode)), HTTPHeaders: input.HTTPHeaders,
		EventMode: strings.TrimSpace(input.EventMode), IncludeDeploymentDetails: input.IncludeDeploymentDetails,
		IncludeCertificateSerial: input.IncludeCertificateSerial, IncludeManagementURL: input.IncludeManagementURL,
		SenderName: strings.TrimSpace(input.SenderName), SenderEmail: strings.TrimSpace(input.SenderEmail),
		Recipients: input.Recipients, TimeoutSecs: input.TimeoutSecs,
		RetryCount: input.RetryCount, RetryIntervalsSecs: input.RetryIntervalsSecs,
		SMTPHost: strings.TrimSpace(input.SMTPHost), SMTPPort: input.SMTPPort,
		SMTPUsername: strings.TrimSpace(input.SMTPUsername), SMTPMode: strings.ToLower(strings.TrimSpace(input.SMTPMode)),
		SMTPAllowPlaintextAuth: input.SMTPAllowPlaintextAuth,
	}
	if candidate.HTTPAuthMode == "" || !candidate.HTTPEnabled {
		candidate.HTTPAuthMode = "none"
	}
	if candidate.HTTPPath == "" {
		candidate.HTTPPath = "/api/email/send"
	}
	if candidate.HTTPLoginPath == "" {
		candidate.HTTPLoginPath = "/api/login"
	}
	if candidate.EventMode == "" {
		candidate.EventMode = "detailed"
	}
	if candidate.SenderName == "" {
		candidate.SenderName = "Scert"
	}
	if candidate.TimeoutSecs == 0 {
		candidate.TimeoutSecs = 10
	}
	if candidate.SMTPPort == 0 {
		candidate.SMTPPort = 587
	}
	if candidate.SMTPMode == "" {
		candidate.SMTPMode = "starttls"
	}
	if candidate.RetryCount < 0 || len(candidate.RetryIntervalsSecs) != candidate.RetryCount || candidate.TimeoutSecs < 1 {
		return candidate, nil, completeError(CompleteErrorInput, "通知超时或重试参数无效", nil)
	}
	if candidate.RetryCount == 0 {
		candidate.RetryIntervalsSecs = []int{}
	}
	if candidate.HTTPEnabled && candidate.Endpoint == "" {
		return candidate, nil, completeError(CompleteErrorInput, "启用 HTTP 通知时必须配置服务地址", nil)
	}
	if candidate.SMTPEnabled && (candidate.SMTPHost == "" || candidate.SMTPUsername == "" || input.SMTPPassword == "" ||
		(candidate.SMTPMode == "plain" && !candidate.SMTPAllowPlaintextAuth)) {
		return candidate, nil, completeError(CompleteErrorInput, "SMTP 通知主机、账号、密码或连接安全参数无效", nil)
	}
	credentials := make(map[string]map[string]string)
	if candidate.HTTPEnabled && candidate.HTTPAuthMode != "none" {
		values, err := notificationCredentials(candidate.HTTPAuthMode, input.HTTPCredentials)
		if err != nil {
			return candidate, nil, completeError(CompleteErrorInput, "HTTP 通知认证凭据无效", err)
		}
		credentials["http"] = values
	}
	if candidate.SMTPEnabled {
		credentials["smtp"] = map[string]string{"username": candidate.SMTPUsername, "password": input.SMTPPassword}
	}
	if len(credentials) == 0 {
		return candidate, nil, nil
	}
	store, ok := completer.credentials.(DatabaseSecretStore)
	if !ok {
		return candidate, nil, completeError(CompleteErrorInternal, "通知加密凭据存储尚未就绪", nil)
	}
	references := make([]string, 0, len(credentials))
	cleanup := sync.OnceValue(func() error {
		var failures []error
		for _, reference := range references {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			failures = append(failures, store.Delete(cleanupCtx, reference))
			cancel()
		}
		return errors.Join(failures...)
	})
	for _, kind := range []string{"http", "smtp"} {
		values, exists := credentials[kind]
		if !exists {
			continue
		}
		token, err := randomToken(24)
		if err != nil {
			return candidate, cleanup, completeError(CompleteErrorInternal, "生成通知凭据引用失败", err)
		}
		reference := "secret://notification/" + token
		references = append(references, reference)
		if err := store.Put(ctx, reference, values); err != nil {
			return candidate, cleanup, completeError(CompleteErrorDependency, "加密保存通知凭据失败", err)
		}
		if kind == "http" {
			candidate.HTTPCredentialRef = reference
		} else {
			candidate.SMTPCredentialRef = reference
		}
	}
	return candidate, cleanup, nil
}

func notificationCredentials(mode string, input map[string]string) (map[string]string, error) {
	first := func(keys ...string) string {
		for _, key := range keys {
			if value := input[key]; strings.TrimSpace(value) != "" {
				return value
			}
		}
		return ""
	}
	switch mode {
	case "bearer":
		if value := first("token", "access_token"); value != "" {
			return map[string]string{"token": value}, nil
		}
	case "hmac_sha256":
		if value := first("secret", "hmac_secret", "key", "token"); value != "" {
			return map[string]string{"secret": value}, nil
		}
	case "session":
		if account := first("account", "username", "identifier"); account != "" && input["password"] != "" {
			return map[string]string{"account": account, "password": input["password"]}, nil
		}
	}
	return nil, errors.New("通知认证方式或必填凭据无效")
}

func applyNotificationInput(input *setup.Input, notification config.NotificationConfig) {
	input.EmailHTTPEnabled = notification.HTTPEnabled
	input.EmailSMTPEnabled = notification.SMTPEnabled
	input.EmailSMTPHost = notification.SMTPHost
	input.EmailSMTPPort = notification.SMTPPort
	input.EmailSMTPUsername = notification.SMTPUsername
	input.EmailSMTPCredentialRef = notification.SMTPCredentialRef
	input.EmailSMTPMode = notification.SMTPMode
	input.EmailSMTPAllowPlaintextAuth = notification.SMTPAllowPlaintextAuth
	input.EmailProvider = notification.Provider
	input.EmailEndpoint = notification.Endpoint
	input.EmailHTTPPath = notification.HTTPPath
	input.EmailHTTPLoginPath = notification.HTTPLoginPath
	input.EmailHTTPAuthMode = notification.HTTPAuthMode
	input.EmailHTTPCredentialRef = notification.HTTPCredentialRef
	input.EmailHTTPHeaders = notification.HTTPHeaders
	input.EmailEventMode = notification.EventMode
	input.EmailIncludeDeploymentDetails = notification.IncludeDeploymentDetails
	input.EmailIncludeCertificateSerial = notification.IncludeCertificateSerial
	input.EmailIncludeManagementURL = notification.IncludeManagementURL
	input.EmailSenderName = notification.SenderName
	input.EmailSenderEmail = notification.SenderEmail
	input.EmailRecipients = notification.Recipients
	input.EmailTimeoutSecs = notification.TimeoutSecs
	input.EmailRetryCount = notification.RetryCount
	input.EmailRetryIntervalsSecs = notification.RetryIntervalsSecs
}
