package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
	"git.pcux.cn/xuans/Scert/backend/internal/passwordpolicy"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

type resetPageData struct {
	CSRFToken string
	Message   string
	Success   bool
}

func (s *Server) currentResetSession() (admin.State, bool) {
	state, err := admin.ReadState(s.resetStatePath)
	if err != nil || !state.Active || state.CodeHash == "" || !s.now().UTC().Before(state.ExpiresAt) {
		return admin.State{}, false
	}
	if state.CodeHash == s.resetClosedHash {
		return admin.State{}, false
	}
	return state, true
}

func newCSRFToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func (s *Server) setResetCSRFCookie(c *gin.Context, token string, expiresAt time.Time) {
	maxAge := int(expiresAt.Sub(s.now().UTC()).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(resetCSRFTokenName, token, maxAge, admin.ResetPath, "", resetCookieSecure(c.Request), true)
}

func resetCookieSecure(request *http.Request) bool {
	if request == nil {
		return true
	}
	if request.TLS != nil {
		return true
	}
	host := request.Host
	if parsedHost, _, err := net.SplitHostPort(request.Host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}
func (s *Server) resetSubmit(c *gin.Context) {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	session, ok := s.currentResetSession()
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	ip := resetClientIP(c.Request)
	now := s.now().UTC()
	if !s.resetLimiter.allowed(ip, now) {
		s.recordResetAudit(c, "blocked", "密码重置请求超过失败频率限制")
		c.Status(http.StatusTooManyRequests)
		return
	}
	if !sameOrigin(c.Request) {
		s.resetLimiter.failed(ip, now)
		s.recordResetAudit(c, "rejected", "密码重置请求来源校验失败")
		s.writeResetFailure(c, http.StatusForbidden, "请求来源校验失败，请从重置页面重新提交")
		return
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		s.resetLimiter.failed(ip, now)
		s.recordResetAudit(c, "rejected", "密码重置请求类型无效")
		s.writeResetFailure(c, http.StatusBadRequest, "请求格式无效，请从重置页面重新提交")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, resetMaxBodyBytes)
	if err := c.Request.ParseForm(); err != nil {
		s.resetLimiter.failed(ip, now)
		s.recordResetAudit(c, "rejected", "密码重置请求内容无效")
		s.writeResetFailure(c, http.StatusBadRequest, "请求内容无效，请稍后重试")
		return
	}
	token := c.Request.PostForm.Get("csrf_token")
	cookie, err := c.Request.Cookie(resetCSRFTokenName)
	if err != nil || subtle.ConstantTimeCompare([]byte(token), []byte(cookie.Value)) != 1 || token == "" {
		s.resetLimiter.failed(ip, now)
		s.recordResetAudit(c, "rejected", "密码重置 CSRF 校验失败")
		s.writeResetFailure(c, http.StatusForbidden, "安全校验失败，请刷新页面后重试")
		return
	}
	code := c.Request.PostForm.Get("reset_code")
	verified, err := admin.VerifyCode(s.resetStatePath, code, now)
	if err != nil || verified.CodeHash != session.CodeHash {
		s.resetLimiter.failed(ip, now)
		s.recordResetAudit(c, "failed", "密码重置校验码无效或已失效")
		s.writeResetFailure(c, http.StatusBadRequest, "重置校验失败，请检查一次性校验码")
		return
	}
	password := c.Request.PostForm.Get("new_password")
	confirmation := c.Request.PostForm.Get("confirm_password")
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()
	if password != confirmation {
		s.recordResetAudit(c, "failed", "密码重置两次输入不一致")
		s.renderResetPage(c, http.StatusBadRequest, token, "两次输入的新密码不一致")
		return
	}
	if err := passwordpolicy.Validate(password, cfg.Admin.Username); err != nil {
		s.recordResetAudit(c, "failed", "密码重置新密码不符合安全规则")
		s.renderResetPage(c, http.StatusBadRequest, token, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.recordResetAudit(c, "error", "生成新密码摘要失败")
		s.writeResetFailure(c, http.StatusInternalServerError, "暂时无法保存新密码，请稍后重试")
		return
	}
	cfg.Admin.PasswordHash = string(hash)
	if strings.TrimSpace(s.configPath) == "" {
		s.recordResetAudit(c, "error", "密码重置配置路径未配置")
		s.writeResetFailure(c, http.StatusInternalServerError, "服务配置路径未就绪，请联系管理员")
		return
	}
	if err := cfg.Save(s.configPath); err != nil {
		s.recordResetAudit(c, "error", "保存密码重置配置失败")
		s.writeResetFailure(c, http.StatusInternalServerError, "暂时无法保存新密码，请稍后重试")
		return
	}
	if err := admin.Consume(s.resetStatePath, verified, now); err != nil {
		s.resetClosedHash = session.CodeHash
		s.configMu.Lock()
		s.config = cfg
		s.configMu.Unlock()
		s.recordResetAudit(c, "error", "新密码已保存但重置入口关闭失败")
		s.clearResetCookie(c)
		s.renderResetSuccess(c, "密码已更新，但重置入口关闭状态需要由 Scert-admin 停止后完成")
		return
	}
	s.configMu.Lock()
	s.config = cfg
	s.configMu.Unlock()
	s.resetClosedHash = session.CodeHash
	s.resetLimiter.clear(ip)
	s.recordResetAudit(c, "success", "超级管理员密码重置成功")
	s.clearResetCookie(c)
	s.renderResetSuccess(c, "密码重置成功，请停止 Scert-admin 以结束本次维护窗口")
}

func sameOrigin(request *http.Request) bool {
	if request == nil || strings.TrimSpace(request.Host) == "" {
		return false
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(request.Header.Get("Referer"))
	}
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, scheme) {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

func (s *Server) clearResetCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(resetCSRFTokenName, "", -1, admin.ResetPath, "", resetCookieSecure(c.Request), true)
}

func (s *Server) writeResetFailure(c *gin.Context, status int, message string) {
	token := ""
	if cookie, err := c.Request.Cookie(resetCSRFTokenName); err == nil {
		token = cookie.Value
	}
	s.renderResetPage(c, status, token, message)
}

func (s *Server) renderResetPage(c *gin.Context, status int, token, message string) {
	s.setResetHeaders(c)
	data := resetPageData{CSRFToken: token, Message: message}
	c.Status(status)
	if err := resetTemplate.Execute(c.Writer, data); err != nil && s.logger != nil {
		s.logger.Error("渲染密码重置页面失败", "error", err)
	}
}

func (s *Server) renderResetSuccess(c *gin.Context, message string) {
	s.setResetHeaders(c)
	if err := resetSuccessTemplate.Execute(c.Writer, resetPageData{Message: message, Success: true}); err != nil && s.logger != nil {
		s.logger.Error("渲染密码重置成功页面失败", "error", err)
	}
	c.Status(http.StatusOK)
}

func (s *Server) setResetHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("X-Robots-Tag", "noindex, nofollow, noarchive")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	c.Header("Content-Type", "text/html; charset=utf-8")
}

func (s *Server) recordResetAudit(c *gin.Context, status, message string) {
	if s.audit == nil {
		return
	}
	err := s.audit.CreateAuditLog(c.Request.Context(), repository.AuditLog{
		Action: "admin_password_reset", ResourceType: "super_admin", ResourceID: "super-admin",
		Status: status, Message: message, CreatedAt: s.now().UTC(),
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("写入密码重置审计日志失败", "error", err)
	}
}

var resetTemplate = template.Must(template.New("reset").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow,noarchive"><title>Scert 超级管理员密码重置</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f4f7fb;color:#172033;font-family:system-ui,-apple-system,"Segoe UI",sans-serif}.card{width:min(520px,calc(100% - 40px));padding:34px;border:1px solid #dce5f1;border-radius:20px;background:#fff;box-shadow:0 18px 60px rgba(31,50,87,.12)}h1{margin:0 0 14px;font-size:26px}p{margin:8px 0;color:#52637b;line-height:1.7}label{display:block;margin-top:16px;font-weight:700}input{box-sizing:border-box;width:100%;margin-top:8px;padding:12px;border:1px solid #c8d4e3;border-radius:10px;font:inherit}.button{width:100%;margin-top:22px;padding:12px;border:0;border-radius:10px;background:#2167d5;color:#fff;font:inherit;font-weight:700;cursor:pointer}.notice{margin-top:18px;padding:14px 16px;border-radius:12px;background:#edf5ff;color:#285a99;font-weight:700}.error{background:#fff1f0;color:#a12b22}</style></head><body><main class="card"><h1>超级管理员密码重置</h1><p>本入口由 Scert-admin 临时开启，仅允许使用一次性校验码重置密码。</p>{{if .Message}}<div class="notice {{if not .Success}}error{{end}}">{{.Message}}</div>{{end}}<form method="post" action="/reset" autocomplete="off"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><label for="reset-code">一次性校验码</label><input id="reset-code" name="reset_code" required maxlength="32" autocomplete="one-time-code"><label for="new-password">新密码</label><input id="new-password" name="new_password" required minlength="8" maxlength="128" type="password" autocomplete="new-password"><label for="confirm-password">确认新密码</label><input id="confirm-password" name="confirm_password" required minlength="8" maxlength="128" type="password" autocomplete="new-password"><button class="button" type="submit">确认重置密码</button></form></main></body></html>`))

var resetSuccessTemplate = template.Must(template.New("reset-success").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Scert 密码重置完成</title></head><body><main><h1>密码重置完成</h1><p>{{.Message}}</p></main></body></html>`))
