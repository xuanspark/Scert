package httpapi

import (
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// frontend 为浏览器提供前端静态文件，并为 Vue 的 history 路由回退到 index.html。
func (s *Server) frontend(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusNotFound)
		return
	}

	s.configMu.RLock()
	enabled := s.config.Web.Enabled
	s.configMu.RUnlock()
	if !enabled || strings.TrimSpace(s.frontendDir) == "" {
		c.Status(http.StatusNotFound)
		return
	}

	requestPath := pathpkg.Clean("/" + strings.TrimPrefix(c.Request.URL.Path, "/"))
	if requestPath == "." || requestPath == "/" {
		requestPath = "/"
	}
	if isCoreRequestPath(requestPath) {
		c.Status(http.StatusNotFound)
		return
	}

	staticDir, err := filepath.Abs(s.frontendDir)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	relativePath := strings.TrimPrefix(requestPath, "/")
	candidate := filepath.Join(staticDir, filepath.FromSlash(relativePath))
	if !isWithinDirectory(staticDir, candidate) {
		c.Status(http.StatusNotFound)
		return
	}

	if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
		serveFrontendFile(c, candidate)
		return
	}

	if requestPath != "/" && filepath.Ext(requestPath) != "" {
		c.Status(http.StatusNotFound)
		return
	}

	indexPath := filepath.Join(staticDir, "index.html")
	if info, statErr := os.Stat(indexPath); statErr != nil || !info.Mode().IsRegular() {
		c.Status(http.StatusNotFound)
		return
	}
	serveFrontendFile(c, indexPath)
}

func serveFrontendFile(c *gin.Context, path string) {
	c.Header("Cache-Control", "no-cache")
	if filepath.Base(path) != "index.html" {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFile(c.Writer, c.Request, path)
}

func isCoreRequestPath(path string) bool {
	return path == "/api" ||
		strings.HasPrefix(path, "/api/") ||
		path == "/healthz" ||
		path == "/readyz" ||
		path == "/reset" ||
		strings.HasPrefix(path, "/reset/")
}

func isWithinDirectory(directory, candidate string) bool {
	relative, err := filepath.Rel(directory, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
