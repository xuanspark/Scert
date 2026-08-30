package httpapi

import (
	"context"
	"errors"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// saveSettingsNotificationCredentials 将页面提交的通知密码写入加密存储，配置文件只保存引用。
func (s *Server) saveSettingsNotificationCredentials(ctx context.Context, cfg *config.Config, request settingsRequest) ([]string, error) {
	createdCredentialRefs := make([]string, 0, 2)
	if cfg == nil || request.Notification == nil {
		return createdCredentialRefs, nil
	}
	item := request.Notification
	if len(item.HTTPCredentials) > 0 {
		if s.secrets == nil {
			return createdCredentialRefs, errors.New("HTTP 通知敏感凭据存储尚未初始化")
		}
		values, err := normalizeNotificationCredentials(cfg.Notification.HTTPAuthMode, item.HTTPCredentials)
		if err != nil {
			return createdCredentialRefs, err
		}
		ref, err := newNotificationCredentialReference()
		if err != nil {
			return createdCredentialRefs, err
		}
		if err := s.secrets.Put(ctx, ref, values); err != nil {
			return createdCredentialRefs, err
		}
		createdCredentialRefs = append(createdCredentialRefs, ref)
		cfg.Notification.HTTPCredentialRef = ref
	}
	if item.SMTPPassword == nil || *item.SMTPPassword == "" {
		return createdCredentialRefs, nil
	}
	if s.secrets == nil {
		return createdCredentialRefs, errors.New("SMTP 通知敏感凭据存储尚未初始化")
	}
	username := strings.TrimSpace(cfg.Notification.SMTPUsername)
	if username == "" {
		return createdCredentialRefs, errors.New("填写 SMTP 密码前必须配置 SMTP 用户名")
	}
	ref, err := newNotificationCredentialReference()
	if err != nil {
		return createdCredentialRefs, err
	}
	values := map[string]string{"username": username, "password": *item.SMTPPassword}
	if err := s.secrets.Put(ctx, ref, values); err != nil {
		return createdCredentialRefs, err
	}
	createdCredentialRefs = append(createdCredentialRefs, ref)
	cfg.Notification.SMTPCredentialRef = ref
	return createdCredentialRefs, nil
}
