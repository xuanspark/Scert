package httpapi

import (
	"net/http"
	"net/http/httptest"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func readyTestConfig(directory string) config.Config {
	cfg := config.Default(directory)
	cfg.SetupReady = true
	return cfg
}

func serveAdminHTTP(server *Server, recorder *httptest.ResponseRecorder, request *http.Request) {
	request.AddCookie(httpUserSessionCookie(server, "test-admin", repository.UserRoleAdmin))
	server.Handler().ServeHTTP(recorder, request)
}
