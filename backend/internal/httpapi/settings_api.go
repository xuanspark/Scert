package httpapi

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

type settingsView struct {
	Server       settingsServerView       `json:"server"`
	Web          settingsWebView          `json:"web"`
	Paths        settingsPathsView        `json:"paths"`
	ACME         settingsACMEView         `json:"acme"`
	DNS          settingsDNSView          `json:"dns"`
	Notification settingsNotificationView `json:"notification"`
}

type settingsServerView struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	HTTPSMode string `json:"https_mode"`
}

type settingsWebView struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
	PublicIP string `json:"public_ip"`
}

type settingsPathsView struct {
	Certificate string `json:"certificate"`
}

type settingsACMEView struct {
	CA                             string `json:"ca"`
	Environment                    string `json:"environment"`
	AccountEmail                   string `json:"account_email"`
	RenewalThresholdDays           int    `json:"renewal_threshold_days"`
	PropagationTimeoutSecs         int    `json:"propagation_timeout_secs"`
	PropagationPollingIntervalSecs int    `json:"propagation_polling_interval_secs"`
}

type settingsDNSView struct {
	Provider          string `json:"provider"`
	VerificationState string `json:"verification_state"`
}

type settingsNotificationView struct {
	HTTPEnabled              bool     `json:"http_enabled"`
	SMTPEnabled              bool     `json:"smtp_enabled"`
	SMTPHost                 string   `json:"smtp_host"`
	SMTPPort                 int      `json:"smtp_port"`
	SMTPUsername             string   `json:"smtp_username"`
	SMTPMode                 string   `json:"smtp_mode"`
	SMTPAllowPlaintextAuth   bool     `json:"smtp_allow_plaintext_auth"`
	SMTPCredentialConfigured bool     `json:"smtp_credential_configured"`
	Provider                 string   `json:"provider"`
	Endpoint                 string   `json:"endpoint"`
	HTTPPath                 string   `json:"http_path"`
	HTTPLoginPath            string   `json:"http_login_path"`
	HTTPAuthMode             string   `json:"http_auth_mode"`
	HTTPCredentialConfigured bool     `json:"http_credential_configured"`
	EventMode                string   `json:"event_mode"`
	IncludeDeploymentDetails bool     `json:"include_deployment_details"`
	IncludeCertificateSerial bool     `json:"include_certificate_serial"`
	IncludeManagementURL     bool     `json:"include_management_url"`
	SenderName               string   `json:"sender_name"`
	SenderEmail              string   `json:"sender_email"`
	Recipients               []string `json:"recipients"`
	TimeoutSecs              int      `json:"timeout_secs"`
	RetryCount               int      `json:"retry_count"`
	RetryIntervalsSecs       []int    `json:"retry_intervals_secs"`
}

type settingsRequest struct {
	Server       *settingsServerRequest       `json:"server"`
	Web          *settingsWebRequest          `json:"web"`
	Paths        *settingsPathsRequest        `json:"paths"`
	ACME         *settingsACMERequest         `json:"acme"`
	DNS          *settingsDNSRequest          `json:"dns"`
	Notification *settingsNotificationRequest `json:"notification"`
}

type settingsServerRequest struct {
	Host      *string `json:"host"`
	Port      *int    `json:"port"`
	HTTPSMode *string `json:"https_mode"`
}

type settingsWebRequest struct {
	Enabled  *bool   `json:"enabled"`
	Hostname *string `json:"hostname"`
	PublicIP *string `json:"public_ip"`
}

type settingsPathsRequest struct {
	Certificate *string `json:"certificate"`
}

type settingsACMERequest struct {
	CA                             *string `json:"ca"`
	Environment                    *string `json:"environment"`
	AccountEmail                   *string `json:"account_email"`
	RenewalThresholdDays           *int    `json:"renewal_threshold_days"`
	PropagationTimeoutSecs         *int    `json:"propagation_timeout_secs"`
	PropagationPollingIntervalSecs *int    `json:"propagation_polling_interval_secs"`
}

type settingsDNSRequest struct {
	Provider *string `json:"provider"`
}

type settingsNotificationRequest struct {
	HTTPEnabled              *bool             `json:"http_enabled"`
	SMTPEnabled              *bool             `json:"smtp_enabled"`
	SMTPHost                 *string           `json:"smtp_host"`
	SMTPPort                 *int              `json:"smtp_port"`
	SMTPUsername             *string           `json:"smtp_username"`
	SMTPPassword             *string           `json:"smtp_password"`
	SMTPMode                 *string           `json:"smtp_mode"`
	SMTPAllowPlaintextAuth   *bool             `json:"smtp_allow_plaintext_auth"`
	HTTPCredentials          map[string]string `json:"http_credentials"`
	Provider                 *string           `json:"provider"`
	Endpoint                 *string           `json:"endpoint"`
	HTTPPath                 *string           `json:"http_path"`
	HTTPLoginPath            *string           `json:"http_login_path"`
	HTTPAuthMode             *string           `json:"http_auth_mode"`
	EventMode                *string           `json:"event_mode"`
	IncludeDeploymentDetails *bool             `json:"include_deployment_details"`
	IncludeCertificateSerial *bool             `json:"include_certificate_serial"`
	IncludeManagementURL     *bool             `json:"include_management_url"`
	SenderName               *string           `json:"sender_name"`
	SenderEmail              *string           `json:"sender_email"`
	Recipients               *[]string         `json:"recipients"`
	TimeoutSecs              *int              `json:"timeout_secs"`
	RetryCount               *int              `json:"retry_count"`
	RetryIntervalsSecs       *[]int            `json:"retry_intervals_secs"`
}

func (s *Server) getSettings(c *gin.Context) {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	c.JSON(http.StatusOK, settingsViewFromConfig(cfg))
}

func (s *Server) updateSettings(c *gin.Context) {
	if strings.TrimSpace(s.configPath) == "" {
		s.writeError(c, http.StatusInternalServerError, "配置文件路径未配置", errors.New("配置文件路径为空"))
		return
	}
	var request settingsRequest
	if err := bindJSON(c, &request, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "设置请求格式不正确", err)
		return
	}

	s.configMu.Lock()
	previous := s.config
	updated := s.config
	applySettingsRequest(&updated, request)
	updated.Normalize()
	createdCredentialRefs, err := s.saveSettingsNotificationCredentials(c.Request.Context(), &updated, request)
	if err != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.configMu.Unlock()
		s.writeError(c, http.StatusBadRequest, "保存通知凭据失败", err)
		return
	}
	if err := updated.Validate(); err != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.configMu.Unlock()
		s.writeError(c, http.StatusBadRequest, "设置内容校验失败", err)
		return
	}
	if err := updated.Save(s.configPath); err != nil {
		s.rollbackSetupCredentialsWithLog(createdCredentialRefs)
		s.configMu.Unlock()
		s.writeError(c, http.StatusInternalServerError, "保存设置失败", err)
		return
	}
	restartRequired := previous.Server.Host != updated.Server.Host ||
		previous.Server.Port != updated.Server.Port ||
		previous.Server.HTTPSMode != updated.Server.HTTPSMode ||
		previous.Paths.Certificate != updated.Paths.Certificate ||
		!reflect.DeepEqual(previous.ACME, updated.ACME) ||
		!reflect.DeepEqual(previous.DNS, updated.DNS) ||
		!reflect.DeepEqual(previous.Notification, updated.Notification)
	message := "设置已保存"
	if restartRequired {
		message = "设置已保存；服务监听地址、端口或 HTTPS 模式已变更，请重启 Scert 服务"
	}
	s.config = updated
	s.configMu.Unlock()
	oldCredentialRefs := []string{
		previous.Notification.HTTPCredentialRef,
		previous.Notification.SMTPCredentialRef,
	}
	newCredentialRefs := map[string]struct{}{
		updated.Notification.HTTPCredentialRef: {},
		updated.Notification.SMTPCredentialRef: {},
	}
	var obsoleteCredentialRefs []string
	for _, reference := range oldCredentialRefs {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			continue
		}
		if _, stillUsed := newCredentialRefs[reference]; !stillUsed {
			obsoleteCredentialRefs = append(obsoleteCredentialRefs, reference)
		}
	}
	if len(obsoleteCredentialRefs) > 0 {
		if err := s.rollbackSetupCredentials(obsoleteCredentialRefs); err != nil && s.logger != nil {
			s.logger.Warn("\u6e05\u7406\u5df2\u66ff\u6362\u7684\u901a\u77e5\u654f\u611f\u51ed\u636e\u5931\u8d25", "error", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"settings":         settingsViewFromConfig(updated),
		"restart_required": restartRequired,
		"message":          message,
	})
}

func settingsViewFromConfig(cfg config.Config) settingsView {
	return settingsView{
		Server: settingsServerView{Host: cfg.Server.Host, Port: cfg.Server.Port, HTTPSMode: cfg.Server.HTTPSMode},
		Web:    settingsWebView{Enabled: cfg.Web.Enabled, Hostname: cfg.Web.Hostname, PublicIP: cfg.Web.PublicIP},
		Paths:  settingsPathsView{Certificate: cfg.Paths.Certificate},
		ACME:   settingsACMEView{CA: cfg.ACME.CA, Environment: cfg.ACME.Environment, AccountEmail: cfg.ACME.AccountEmail, RenewalThresholdDays: cfg.ACME.RenewalThresholdDays, PropagationTimeoutSecs: cfg.ACME.PropagationTimeoutSecs, PropagationPollingIntervalSecs: cfg.ACME.PropagationPollingIntervalSecs},
		DNS:    settingsDNSView{Provider: cfg.DNS.Provider, VerificationState: cfg.DNS.VerificationState},
		Notification: settingsNotificationView{
			HTTPEnabled: cfg.Notification.HTTPEnabled, SMTPEnabled: cfg.Notification.SMTPEnabled, Provider: cfg.Notification.Provider,
			SMTPHost: cfg.Notification.SMTPHost, SMTPPort: cfg.Notification.SMTPPort, SMTPUsername: cfg.Notification.SMTPUsername,
			SMTPMode: cfg.Notification.SMTPMode, SMTPAllowPlaintextAuth: cfg.Notification.SMTPAllowPlaintextAuth,
			SMTPCredentialConfigured: strings.TrimSpace(cfg.Notification.SMTPCredentialRef) != "",
			Endpoint:                 cfg.Notification.Endpoint, HTTPPath: cfg.Notification.HTTPPath, HTTPLoginPath: cfg.Notification.HTTPLoginPath,
			HTTPAuthMode: cfg.Notification.HTTPAuthMode, HTTPCredentialConfigured: strings.TrimSpace(cfg.Notification.HTTPCredentialRef) != "",
			EventMode: cfg.Notification.EventMode, IncludeDeploymentDetails: cfg.Notification.IncludeDeploymentDetails,
			IncludeCertificateSerial: cfg.Notification.IncludeCertificateSerial, IncludeManagementURL: cfg.Notification.IncludeManagementURL,
			SenderName: cfg.Notification.SenderName, SenderEmail: cfg.Notification.SenderEmail,
			Recipients: append([]string(nil), cfg.Notification.Recipients...), TimeoutSecs: cfg.Notification.TimeoutSecs,
			RetryCount: cfg.Notification.RetryCount, RetryIntervalsSecs: append([]int(nil), cfg.Notification.RetryIntervalsSecs...),
		},
	}
}

func applySettingsRequest(cfg *config.Config, request settingsRequest) {
	if cfg == nil {
		return
	}
	if request.Server != nil {
		if request.Server.Host != nil {
			cfg.Server.Host = strings.TrimSpace(*request.Server.Host)
		}
		if request.Server.Port != nil {
			cfg.Server.Port = *request.Server.Port
		}
		if request.Server.HTTPSMode != nil {
			cfg.Server.HTTPSMode = strings.TrimSpace(*request.Server.HTTPSMode)
		}
	}
	if request.Web != nil {
		if request.Web.Enabled != nil {
			cfg.Web.Enabled = *request.Web.Enabled
		}
		if request.Web.Hostname != nil {
			cfg.Web.Hostname = strings.TrimSpace(*request.Web.Hostname)
		}
		if request.Web.PublicIP != nil {
			cfg.Web.PublicIP = strings.TrimSpace(*request.Web.PublicIP)
		}
	}
	if request.Paths != nil && request.Paths.Certificate != nil {
		cfg.Paths.Certificate = strings.TrimSpace(*request.Paths.Certificate)
	}
	if request.ACME != nil {
		if request.ACME.CA != nil {
			cfg.ACME.CA = strings.TrimSpace(*request.ACME.CA)
		}
		if request.ACME.Environment != nil {
			cfg.ACME.Environment = strings.TrimSpace(*request.ACME.Environment)
		}
		if request.ACME.AccountEmail != nil {
			cfg.ACME.AccountEmail = strings.TrimSpace(*request.ACME.AccountEmail)
		}
		if request.ACME.RenewalThresholdDays != nil {
			cfg.ACME.RenewalThresholdDays = *request.ACME.RenewalThresholdDays
		}
		if request.ACME.PropagationTimeoutSecs != nil {
			cfg.ACME.PropagationTimeoutSecs = *request.ACME.PropagationTimeoutSecs
		}
		if request.ACME.PropagationPollingIntervalSecs != nil {
			cfg.ACME.PropagationPollingIntervalSecs = *request.ACME.PropagationPollingIntervalSecs
		}
	}
	if request.DNS != nil && request.DNS.Provider != nil {
		cfg.DNS.Provider = strings.TrimSpace(*request.DNS.Provider)
	}
	if request.Notification != nil {
		item := request.Notification
		if item.HTTPEnabled != nil {
			cfg.Notification.HTTPEnabled = *item.HTTPEnabled
		}
		if item.SMTPEnabled != nil {
			cfg.Notification.SMTPEnabled = *item.SMTPEnabled
		}
		if item.SMTPHost != nil {
			cfg.Notification.SMTPHost = strings.TrimSpace(*item.SMTPHost)
		}
		if item.SMTPPort != nil {
			cfg.Notification.SMTPPort = *item.SMTPPort
		}
		if item.SMTPUsername != nil {
			cfg.Notification.SMTPUsername = strings.TrimSpace(*item.SMTPUsername)
		}
		if item.SMTPMode != nil {
			cfg.Notification.SMTPMode = strings.TrimSpace(*item.SMTPMode)
		}
		if item.SMTPAllowPlaintextAuth != nil {
			cfg.Notification.SMTPAllowPlaintextAuth = *item.SMTPAllowPlaintextAuth
		}
		if item.Provider != nil {
			cfg.Notification.Provider = strings.TrimSpace(*item.Provider)
		}
		if item.Endpoint != nil {
			cfg.Notification.Endpoint = strings.TrimSpace(*item.Endpoint)
		}
		if item.HTTPPath != nil {
			cfg.Notification.HTTPPath = strings.TrimSpace(*item.HTTPPath)
		}
		if item.HTTPLoginPath != nil {
			cfg.Notification.HTTPLoginPath = strings.TrimSpace(*item.HTTPLoginPath)
		}
		if item.HTTPAuthMode != nil {
			cfg.Notification.HTTPAuthMode = strings.TrimSpace(*item.HTTPAuthMode)
		}
		if item.EventMode != nil {
			cfg.Notification.EventMode = strings.TrimSpace(*item.EventMode)
		}
		if item.IncludeDeploymentDetails != nil {
			cfg.Notification.IncludeDeploymentDetails = *item.IncludeDeploymentDetails
		}
		if item.IncludeCertificateSerial != nil {
			cfg.Notification.IncludeCertificateSerial = *item.IncludeCertificateSerial
		}
		if item.IncludeManagementURL != nil {
			cfg.Notification.IncludeManagementURL = *item.IncludeManagementURL
		}
		if item.SenderName != nil {
			cfg.Notification.SenderName = strings.TrimSpace(*item.SenderName)
		}
		if item.SenderEmail != nil {
			cfg.Notification.SenderEmail = strings.TrimSpace(*item.SenderEmail)
		}
		if item.Recipients != nil {
			cfg.Notification.Recipients = append([]string(nil), (*item.Recipients)...)
		}
		if item.TimeoutSecs != nil {
			cfg.Notification.TimeoutSecs = *item.TimeoutSecs
		}
		if item.RetryCount != nil {
			cfg.Notification.RetryCount = *item.RetryCount
		}
		if item.RetryIntervalsSecs != nil {
			cfg.Notification.RetryIntervalsSecs = append([]int(nil), (*item.RetryIntervalsSecs)...)
		}
	}
}
