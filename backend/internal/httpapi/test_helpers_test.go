package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func readyTestConfig(directory string) config.Config {
	cfg := config.Default(directory)
	// Linux 默认目录属于正式安装，测试必须显式隔离所有可写路径。
	cfg.Paths = config.PathsConfig{
		Install: directory, Config: filepath.Join(directory, "conf"),
		Data: filepath.Join(directory, "data"), Log: filepath.Join(directory, "logs"),
		Certificate: filepath.Join(directory, "ssl"), Backup: filepath.Join(directory, "backups"),
	}
	cfg.Database.DSN = filepath.Join(cfg.Paths.Data, "scert.db")
	cfg.SetupReady = true
	return cfg
}

func serveAdminHTTP(server *Server, recorder *httptest.ResponseRecorder, request *http.Request) {
	request.AddCookie(httpUserSessionCookie(server, "test-admin", repository.UserRoleAdmin))
	server.Handler().ServeHTTP(recorder, request)
}
