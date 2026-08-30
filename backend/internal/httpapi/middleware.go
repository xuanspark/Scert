package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

const requestIDHeader = "X-Scert-Request-ID"
const jsonRequestBodyLimit = 64 * 1024

func (s *Server) requestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader(requestIDHeader))
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		c.Set("request_id", requestID)
		c.Header(requestIDHeader, requestID)
		started := time.Now()
		c.Next()
		if s.logger != nil {
			s.logger.Info("HTTP 请求完成",
				"request_id", requestID,
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"status", c.Writer.Status(),
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}
	}
}

func (s *Server) requireAdmin(c *gin.Context) {
	s.configMu.RLock()
	setupReady := s.config.SetupReady
	s.configMu.RUnlock()
	if !setupReady {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error":      "请先完成初始化配置",
			"request_id": requestIDFromContext(c),
		})
		return
	}
	session, ok := s.currentAuthSession(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error":      "请先登录管理后台",
			"request_id": requestIDFromContext(c),
		})
		return
	}
	if session.Role != repository.UserRoleSuperAdmin && session.Role != repository.UserRoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":      "当前账号没有管理权限",
			"request_id": requestIDFromContext(c),
		})
		return
	}
	if !s.sameOriginForAPI(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":      "请求来源校验失败",
			"request_id": requestIDFromContext(c),
		})
		return
	}
	c.Next()
}

func (s *Server) sameOriginForAPI(request *http.Request) bool {
	if request == nil {
		return false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(request.Header.Get("Referer"))
	}
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	requestScheme := "http"
	if s.requestIsHTTPS(request) {
		requestScheme = "https"
	}
	return strings.EqualFold(parsed.Host, request.Host) && strings.EqualFold(parsed.Scheme, requestScheme)
}

func requestIDFromContext(c *gin.Context) string {
	value, exists := c.Get("request_id")
	if !exists {
		return ""
	}
	requestID, _ := value.(string)
	return requestID
}

func validRequestID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "scert-request"
	}
	return "scert-" + hex.EncodeToString(buffer)
}
