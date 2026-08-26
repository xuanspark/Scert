package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
)

func TestHealthAndDomainAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Database.DSN = filepath.Join(dir, "data", "test.db")
	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("数据库迁移失败：%v", err)
	}
	logger, err := logging.New(filepath.Join(dir, "logs"), 0)
	if err != nil {
		t.Fatalf("初始化日志失败：%v", err)
	}
	defer logger.Close()
	server := New(Dependencies{Config: cfg, Logger: logger, Repository: repository.NewSQLDomainRepository(db.DB)})

	health := httptest.NewRecorder()
	server.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("健康检查响应错误：%d %s", health.Code, health.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"single"}`))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	server.Handler().ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("创建域名响应错误：%d %s", created.Code, created.Body.String())
	}

	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "example.com") {
		t.Fatalf("域名列表响应错误：%d %s", list.Code, list.Body.String())
	}
}
