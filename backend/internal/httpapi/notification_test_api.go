package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/notification"
)

type notificationTestRequest struct {
	SMTPEnabled            bool              `json:"smtp_enabled"`
	SMTPHost               string            `json:"smtp_host"`
	SMTPPort               int               `json:"smtp_port"`
	SMTPUsername           string            `json:"smtp_username"`
	SMTPPassword           string            `json:"smtp_password"`
	SMTPMode               string            `json:"smtp_mode"`
	SMTPAllowPlaintextAuth bool              `json:"smtp_allow_plaintext_auth"`
	Recipient              string            `json:"recipient"`
	HTTPEnabled            bool              `json:"http_enabled"`
	HTTPCredentials        map[string]string `json:"http_credentials"`
	Provider               string            `json:"provider"`
	Endpoint               string            `json:"endpoint"`
	HTTPPath               string            `json:"http_path"`
	HTTPLoginPath          string            `json:"http_login_path"`
	HTTPAuthMode           string            `json:"http_auth_mode"`
}

type memoryCredentialReader struct {
	creds map[string]map[string]string
}

func (m *memoryCredentialReader) Get(ctx context.Context, ref string) (map[string]string, error) {
	if m == nil || m.creds == nil {
		return nil, errors.New("凭据不存在")
	}
	val, ok := m.creds[ref]
	if !ok {
		return nil, errors.New("凭据不存在")
	}
	return val, nil
}

func (s *Server) testNotification(c *gin.Context) {
	_, authenticated := s.currentAuthSession(c)
	if !authenticated {
		s.writeError(c, http.StatusUnauthorized, "请先登录管理后台", nil)
		return
	}
	var req notificationTestRequest
	if err := bindJSON(c, &req, jsonRequestBodyLimit); err != nil {
		s.writeError(c, http.StatusBadRequest, "测试请求参数无效", err)
		return
	}

	targetRecipient := strings.TrimSpace(req.Recipient)
	if targetRecipient == "" {
		s.writeError(c, http.StatusBadRequest, "请输入测试收件人邮箱", nil)
		return
	}

	s.configMu.RLock()
	currentCfg := s.config.Notification
	s.configMu.RUnlock()

	testCfg := currentCfg
	testCfg.SMTPEnabled = req.SMTPEnabled
	if req.SMTPHost != "" {
		testCfg.SMTPHost = strings.TrimSpace(req.SMTPHost)
	}
	if req.SMTPPort > 0 {
		testCfg.SMTPPort = req.SMTPPort
	}
	if req.SMTPUsername != "" {
		testCfg.SMTPUsername = strings.TrimSpace(req.SMTPUsername)
	}
	if req.SMTPMode != "" {
		testCfg.SMTPMode = strings.TrimSpace(req.SMTPMode)
	}
	testCfg.SMTPAllowPlaintextAuth = req.SMTPAllowPlaintextAuth

	credsMap := make(map[string]map[string]string)
	smtpPassword := strings.TrimSpace(req.SMTPPassword)
	if smtpPassword != "" {
		testRef := "secret://test/smtp"
		testCfg.SMTPCredentialRef = testRef
		credsMap[testRef] = map[string]string{
			"username": testCfg.SMTPUsername,
			"password": smtpPassword,
		}
	} else if testCfg.SMTPCredentialRef != "" {
		if reader, ok := s.secrets.(readinessSecretReader); ok && reader != nil {
			if realCreds, err := reader.Get(c.Request.Context(), testCfg.SMTPCredentialRef); err == nil && realCreds != nil {
				credsMap[testCfg.SMTPCredentialRef] = realCreds
			}
		}
	}

	if testCfg.SMTPEnabled && credsMap[testCfg.SMTPCredentialRef] == nil && testCfg.UsesSmailSystemAccount() {
		s.writeError(c, http.StatusBadRequest, "尚未配置 SMTP 密码，请先输入密码再进行测试", nil)
		return
	}

	reader := &memoryCredentialReader{creds: credsMap}
	notifService := notification.New(testCfg, reader)

	testCtx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	if err := notifService.SendTestMail(testCtx, targetRecipient); err != nil {
		s.writeError(c, http.StatusUnprocessableEntity, "邮件测试发送失败："+err.Error(), err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "测试邮件已发送，请检查收件箱",
		"recipient": targetRecipient,
	})
}
