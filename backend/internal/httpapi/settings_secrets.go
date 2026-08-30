package httpapi

import (
	"context"
	"errors"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
)

// saveSettingsNotificationCredentials 将页面提交的通知密码写入加密存储，配置文件只保存引用。
func (s *Server) saveSettingsNotificationCredentials(ctx context.Context, cfg *config.Config, request settingsRequest) error {
	if cfg == nil || request.Notification == nil {
		return nil
	}
	item := request.Notification
	if len(item.HTTPCredentials) > 0 {
		if s.secrets == nil {
			return errors.New("HTTP 通知敏感凭据存储尚未初始化")
		}
		values, err := normalizeNotificationCredentials(cfg.Notification.HTTPAuthMode, item.HTTPCredentials)
		if err != nil {
			return err
		}
		ref, err := newNotificationCredentialReference()
		if err != nil {
			return err
		}
		if err := s.secrets.Put(ctx, ref, values); err != nil {
			return err
		}
		cfg.Notification.HTTPCredentialRef = ref
	}
	if item.SMTPPassword == nil || *item.SMTPPassword == "" {
		return nil
	}
	if s.secrets == nil {
		return errors.New("SMTP 通知敏感凭据存储尚未初始化")
	}
	username := strings.TrimSpace(cfg.Notification.SMTPUsername)
	if username == "" {
		return errors.New("填写 SMTP 密码前必须配置 SMTP 用户名")
	}
	ref, err := newNotificationCredentialReference()
	if err != nil {
		return err
	}
	values := map[string]string{"username": username, "password": *item.SMTPPassword}
	if err := s.secrets.Put(ctx, ref, values); err != nil {
		return err
	}
	cfg.Notification.SMTPCredentialRef = ref
	return nil
}
