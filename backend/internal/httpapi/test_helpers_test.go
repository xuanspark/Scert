package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

// 仅供旧的响应格式与错误脱敏单元测试显式注入；权限验证使用真实 SQL 资源夹具。
// 不在产品构造器或通用请求帮助函数中自动补齐，缺失依赖测试必须保持拒绝。
type projectResourceReaderStub struct {
	domains repository.DomainRepository
	servers repository.ServerRepository
}

func (r projectResourceReaderStub) ListVisibleDomains(ctx context.Context, _ string) ([]domain.Domain, error) {
	return r.domains.List(ctx)
}

func (r projectResourceReaderStub) GetVisibleDomain(ctx context.Context, _ string, id string) (domain.Domain, error) {
	return r.domains.GetByID(ctx, id)
}

func (r projectResourceReaderStub) ListVisibleServers(ctx context.Context, _ string) ([]deployment.Server, error) {
	return r.servers.ListServers(ctx)
}

func (r projectResourceReaderStub) GetVisibleServer(ctx context.Context, _ string, id string) (deployment.Server, error) {
	return r.servers.GetServerByID(ctx, id)
}

func (r projectResourceReaderStub) ListVisibleDomainServers(ctx context.Context, _ string, id string) ([]deployment.Server, error) {
	return r.servers.ListServersByDomainID(ctx, id)
}

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
