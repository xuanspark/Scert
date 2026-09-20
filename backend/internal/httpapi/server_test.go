package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"git.pcux.cn/xuans/Scert/backend/internal/admin"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
	"git.pcux.cn/xuans/Scert/backend/internal/domain"
	"git.pcux.cn/xuans/Scert/backend/internal/logging"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/sshkey"
)

func TestHealthAndDomainAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
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
	serveAdminHTTP(server, health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("健康检查响应错误：%d %s", health.Code, health.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"single"}`))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	serveAdminHTTP(server, created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("创建域名响应错误：%d %s", created.Code, created.Body.String())
	}

	list := httptest.NewRecorder()
	serveAdminHTTP(server, list, httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "example.com") {
		t.Fatalf("域名列表响应错误：%d %s", list.Code, list.Body.String())
	}
}

func TestLegacyResetPathRequiresScertAdminActivation(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.Server.Port = 8443
	statePath := admin.StatePath(cfg.Paths.Data)
	server := newLegacyResetServer(Dependencies{Config: cfg, Repository: &stubDomainRepository{}, ResetStatePath: statePath})

	inactive := httptest.NewRecorder()
	serveAdminHTTP(server, inactive, httptest.NewRequest(http.MethodGet, "/reset", nil))
	if inactive.Code != http.StatusNotFound {
		t.Fatalf("Scert-admin 未启动时 /reset 应返回 404：%d", inactive.Code)
	}

	controller := admin.New(cfg)
	if _, err := controller.Start(); err != nil {
		t.Fatalf("启用测试密码重置入口失败：%v", err)
	}
	active := httptest.NewRecorder()
	serveAdminHTTP(server, active, httptest.NewRequest(http.MethodGet, "/reset", nil))
	if active.Code != http.StatusOK || !strings.Contains(active.Body.String(), "超级管理员密码重置") {
		t.Fatalf("Scert-admin 启用后 /reset 页面错误：%d %s", active.Code, active.Body.String())
	}
	if active.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate" || !strings.Contains(active.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("密码重置页面缺少安全响应头：%v", active.Header())
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("关闭测试密码重置入口失败：%v", err)
	}
	stopped := httptest.NewRecorder()
	serveAdminHTTP(server, stopped, httptest.NewRequest(http.MethodGet, "/reset", nil))
	if stopped.Code != http.StatusNotFound {
		t.Fatalf("Scert-admin 停止后 /reset 应恢复 404：%d", stopped.Code)
	}
}

func TestLegacyResetPasswordUpdatesConfigAndConsumesSession(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.DNS.VerificationState = "verified"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	oldHash, err := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成旧密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(oldHash)
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	controller := admin.New(cfg)
	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动测试重置会话失败：%v", err)
	}
	server := newLegacyResetServer(Dependencies{Config: cfg, ConfigPath: configPath, Repository: &stubDomainRepository{}, ResetStatePath: admin.StatePath(cfg.Paths.Data)})
	get := httptest.NewRecorder()
	serveAdminHTTP(server, get, httptest.NewRequest(http.MethodGet, "http://scert.example.test/reset", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("获取密码重置页面失败：%d", get.Code)
	}
	cookie := get.Result().Cookies()[0]
	form := url.Values{
		"csrf_token":       {cookie.Value},
		"reset_code":       {session.Code},
		"new_password":     {"NewPass1234"},
		"confirm_password": {"NewPass1234"},
	}
	request := httptest.NewRequest(http.MethodPost, "http://scert.example.test/reset", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://scert.example.test")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "密码重置成功") {
		t.Fatalf("密码重置响应错误：%d %s", response.Code, response.Body.String())
	}
	state, err := admin.ReadState(admin.StatePath(cfg.Paths.Data))
	if err != nil {
		t.Fatalf("读取重置状态失败：%v", err)
	}
	if state.Active || state.CodeHash != "" {
		t.Fatalf("密码重置成功后会话必须失效：%+v", state)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("读取重置后的配置失败：%v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(loaded.Admin.PasswordHash), []byte("NewPass1234")) != nil {
		t.Fatal("新密码摘要无法验证新密码")
	}
	if bcrypt.CompareHashAndPassword([]byte(loaded.Admin.PasswordHash), []byte("OldPassword123")) == nil {
		t.Fatal("旧密码不应继续有效")
	}
	second := httptest.NewRecorder()
	serveAdminHTTP(server, second, httptest.NewRequest(http.MethodGet, "http://scert.example.test/reset", nil))
	if second.Code != http.StatusNotFound {
		t.Fatalf("密码重置成功后入口必须关闭：%d", second.Code)
	}
	if err := controller.Refresh(); err != admin.ErrResetSessionClosed {
		t.Fatalf("Scert-admin 不得刷新已消费会话：%v", err)
	}
}

func TestLegacyResetPasswordSynchronizesPrimarySuperAdminUser(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.DNS.VerificationState = "verified"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	oldHash, err := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成旧密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(oldHash)
	cfg.Database.DSN = filepath.Join(dir, "data", "reset-users.db")
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatalf("打开测试用户数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatalf("迁移测试用户数据库失败：%v", err)
	}
	users := repository.NewSQLUserRepository(db.DB, "sqlite")
	if err := users.EnsureSuperAdmin(t.Context(), repository.User{
		ID: "primary-super-admin", Username: cfg.Admin.Username, Email: cfg.Admin.Email, PasswordHash: cfg.Admin.PasswordHash,
	}); err != nil {
		t.Fatalf("创建测试超级管理员失败：%v", err)
	}
	controller := admin.New(cfg)
	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动测试重置会话失败：%v", err)
	}
	server := newLegacyResetServer(Dependencies{
		Config: cfg, ConfigPath: configPath, Repository: &stubDomainRepository{}, Users: users,
		ResetStatePath: admin.StatePath(cfg.Paths.Data),
	})
	get := httptest.NewRecorder()
	serveAdminHTTP(server, get, httptest.NewRequest(http.MethodGet, "https://scert.example.test/reset", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("获取密码重置页面失败：%d", get.Code)
	}
	cookies := get.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("密码重置页面 Cookie 数量错误：%d", len(cookies))
	}
	form := resetForm(session.Code, cookies[0].Value, "NewPass1234")
	request := httptest.NewRequest(http.MethodPost, "https://scert.example.test/reset", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://scert.example.test")
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("密码重置失败：%d %s", response.Code, response.Body.String())
	}
	item, err := users.GetUserByID(t.Context(), "primary-super-admin")
	if err != nil {
		t.Fatalf("读取同步后的超级管理员失败：%v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(item.PasswordHash), []byte("NewPass1234")) != nil {
		t.Fatal("用户表中的超级管理员摘要未同步新密码")
	}
}

func TestLegacyResetRejectsCrossSiteAndLimitsFailures(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.DNS.VerificationState = "verified"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	cfg.Admin.PasswordHash = "not-used-in-this-test"
	controller := admin.New(cfg)
	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动测试重置会话失败：%v", err)
	}
	server := newLegacyResetServer(Dependencies{Config: cfg, ConfigPath: filepath.Join(dir, "config", "scert.json"), Repository: &stubDomainRepository{}, ResetStatePath: admin.StatePath(cfg.Paths.Data)})
	get := httptest.NewRecorder()
	serveAdminHTTP(server, get, httptest.NewRequest(http.MethodGet, "http://scert.example.test/reset", nil))
	cookie := get.Result().Cookies()[0]
	base := url.Values{"csrf_token": {cookie.Value}, "reset_code": {session.Code}, "new_password": {"NewPass1234"}, "confirm_password": {"NewPass1234"}}
	for i := 0; i < 5; i++ {
		request := httptest.NewRequest(http.MethodPost, "http://scert.example.test/reset", strings.NewReader(base.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "http://evil.example.test")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		serveAdminHTTP(server, response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("跨站请求应被拒绝：第 %d 次状态码为 %d", i+1, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "http://scert.example.test/reset", strings.NewReader(base.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://scert.example.test")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("超过失败次数后应限流：%d", response.Code)
	}
}

func TestServerAndDomainAssociationAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.Database.DSN = filepath.Join(dir, "data", "servers.db")
	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("数据库迁移失败：%v", err)
	}
	secretStore, err := secretstore.Open(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	sshKeys := sshkey.NewService(secretStore)
	key, err := sshKeys.Create(t.Context(), "测试部署私钥", testSSHPrivateKey(t), "")
	if err != nil {
		t.Fatalf("创建测试 SSH 私钥失败：%v", err)
	}
	server := New(Dependencies{
		Config: cfg, Repository: repository.NewSQLDomainRepository(db.DB),
		Servers: repository.NewSQLServerRepository(db.DB), SSHKeys: sshKeys,
	})

	domainRequest := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"example.com","certificate_type":"single"}`))
	domainRequest.Header.Set("Content-Type", "application/json")
	domainResponse := httptest.NewRecorder()
	serveAdminHTTP(server, domainResponse, domainRequest)
	if domainResponse.Code != http.StatusCreated {
		t.Fatalf("创建测试域名失败：%d %s", domainResponse.Code, domainResponse.Body.String())
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(domainResponse.Body.Bytes(), &item); err != nil || item.ID == "" {
		t.Fatalf("测试域名响应无效：%v %s", err, domainResponse.Body.String())
	}

	serverRequest := httptest.NewRequest(http.MethodPost, "/api/v1/servers", strings.NewReader(`{"name":"测试服务器","address":"192.0.2.50","port":22,"username":"deploy","private_key_ref":"`+key.Reference+`","host_key_fingerprint":"SHA256:test","web_server":"nginx"}`))
	serverRequest.Header.Set("Content-Type", "application/json")
	serverResponse := httptest.NewRecorder()
	serveAdminHTTP(server, serverResponse, serverRequest)
	if serverResponse.Code != http.StatusCreated {
		t.Fatalf("创建测试服务器失败：%d %s", serverResponse.Code, serverResponse.Body.String())
	}
	var created deployment.Server
	if err := json.Unmarshal(serverResponse.Body.Bytes(), &created); err != nil || created.ID == "" || len(created.ValidationCommand) != 2 || created.ValidationCommand[1] != "-t" || len(created.ReloadCommand) != 3 {
		t.Fatalf("测试服务器响应无效：%v %+v", err, created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created server response has zero timestamps: %+v", created)
	}
	if created.CertificatePath != deployment.DefaultCertificatePath || created.CertificateChainPath != deployment.DefaultCertificateChainPath || created.PrivateKeyPath != deployment.DefaultPrivateKeyPath {
		t.Fatalf("服务器 API 未使用默认远程证书路径模板：%+v", created)
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/v1/servers/"+created.ID, strings.NewReader(`{"name":"updated-server","address":"192.0.2.50","port":22,"username":"deploy","private_key_ref":"`+key.Reference+`","host_key_fingerprint":"SHA256:test","web_server":"nginx"}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	serveAdminHTTP(server, updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update server failed: %d %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated deployment.Server
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated server response: %v", err)
	}
	if updated.Name != "updated-server" || updated.CreatedAt.IsZero() || updated.UpdatedAt.IsZero() {
		t.Fatalf("updated server response is incomplete: %+v", updated)
	}

	associationRequest := httptest.NewRequest(http.MethodPut, "/api/v1/domains/"+item.ID+"/servers", strings.NewReader(`{"server_ids":["`+created.ID+`"]}`))
	associationRequest.Header.Set("Content-Type", "application/json")
	associationResponse := httptest.NewRecorder()
	serveAdminHTTP(server, associationResponse, associationRequest)
	if associationResponse.Code != http.StatusOK || !strings.Contains(associationResponse.Body.String(), created.ID) {
		t.Fatalf("保存域名服务器关联失败：%d %s", associationResponse.Code, associationResponse.Body.String())
	}

	deleteResponse := httptest.NewRecorder()
	serveAdminHTTP(server, deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/servers/"+created.ID, nil))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("删除测试服务器失败：%d %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	listResponse := httptest.NewRecorder()
	serveAdminHTTP(server, listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/domains/"+item.ID+"/servers", nil))
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), created.ID) {
		t.Fatalf("服务器删除后域名关联未清理：%d %s", listResponse.Code, listResponse.Body.String())
	}
}
func TestLegacyResetConfigSaveFailureKeepsSessionAndPassword(t *testing.T) {
	fixture := newResetTestFixture(t)
	blockedPath := filepath.Join(t.TempDir(), "config-target")
	if err := os.MkdirAll(blockedPath, 0o700); err != nil {
		t.Fatalf("创建无效配置目标失败：%v", err)
	}
	fixture.server.configPath = blockedPath

	form := resetForm(fixture.session.Code, fixture.cookie.Value, "NewPass1234")
	request := httptest.NewRequest(http.MethodPost, "https://scert.example.test/reset", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://scert.example.test")
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("配置保存失败时应返回 500：%d %s", response.Code, response.Body.String())
	}

	state, err := admin.ReadState(admin.StatePath(fixture.cfg.Paths.Data))
	if err != nil {
		t.Fatalf("读取配置保存失败后的重置状态失败：%v", err)
	}
	if !state.Active || state.CodeHash != fixture.session.State.CodeHash {
		t.Fatalf("配置保存失败时不得消费重置会话：%+v", state)
	}
	fixture.server.configMu.RLock()
	passwordHash := fixture.server.config.Admin.PasswordHash
	fixture.server.configMu.RUnlock()
	if passwordHash != fixture.oldPasswordHash {
		t.Fatal("配置保存失败时不得更新内存中的密码摘要")
	}
	active := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(active, httptest.NewRequest(http.MethodGet, "https://scert.example.test/reset", nil))
	if active.Code != http.StatusOK {
		t.Fatalf("配置保存失败后 /reset 应继续可访问：%d", active.Code)
	}
}

func TestLegacyResetRejectsOversizedRequestBody(t *testing.T) {
	fixture := newResetTestFixture(t)
	form := resetForm(fixture.session.Code, fixture.cookie.Value, "NewPass1234")
	form.Set("padding", strings.Repeat("x", resetMaxBodyBytes))
	request := httptest.NewRequest(http.MethodPost, "https://scert.example.test/reset", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://scert.example.test")
	request.AddCookie(fixture.cookie)
	response := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("超过请求体限制时应返回 400：%d %s", response.Code, response.Body.String())
	}
	state, err := admin.ReadState(admin.StatePath(fixture.cfg.Paths.Data))
	if err != nil {
		t.Fatalf("读取超限请求后的重置状态失败：%v", err)
	}
	if !state.Active {
		t.Fatal("请求体超限时不得消费重置会话")
	}
}

func TestLegacyResetCSRFCookieHasRestrictedAttributes(t *testing.T) {
	fixture := newResetTestFixture(t)
	cookie := fixture.cookie
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("CSRF Cookie 安全属性不完整：%+v", cookie)
	}
	if cookie.Path != admin.ResetPath {
		t.Fatalf("CSRF Cookie 路径错误：%q", cookie.Path)
	}
	if cookie.MaxAge <= 0 || cookie.MaxAge > 90 {
		t.Fatalf("CSRF Cookie 有效期必须限制在重置租约内：%d", cookie.MaxAge)
	}
}

func TestLegacyResetConcurrentSubmissionsAllowOnlyOneSuccess(t *testing.T) {
	fixture := newResetTestFixture(t)
	form := resetForm(fixture.session.Code, fixture.cookie.Value, "NewPass1234")
	start := make(chan struct{})
	responses := make(chan int, 2)
	var waitGroup sync.WaitGroup
	for i := 0; i < 2; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			request := httptest.NewRequest(http.MethodPost, "https://scert.example.test/reset", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "https://scert.example.test")
			request.AddCookie(fixture.cookie)
			response := httptest.NewRecorder()
			fixture.server.Handler().ServeHTTP(response, request)
			responses <- response.Code
		}()
	}
	close(start)
	waitGroup.Wait()
	close(responses)

	successes := 0
	failures := 0
	for status := range responses {
		if status == http.StatusOK {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("并发提交必须恰好一个成功、一个失败：successes=%d failures=%d", successes, failures)
	}
	state, err := admin.ReadState(admin.StatePath(fixture.cfg.Paths.Data))
	if err != nil {
		t.Fatalf("读取并发提交后的重置状态失败：%v", err)
	}
	if state.Active || state.CodeHash != "" {
		t.Fatalf("并发提交成功后重置会话必须关闭：%+v", state)
	}
	loaded, err := config.Load(fixture.configPath)
	if err != nil {
		t.Fatalf("读取并发提交后的配置失败：%v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(loaded.Admin.PasswordHash), []byte("NewPass1234")) != nil {
		t.Fatal("并发提交后的密码摘要无法验证新密码")
	}
}

type resetTestFixture struct {
	server          *Server
	cfg             config.Config
	controller      *admin.Controller
	session         admin.Session
	cookie          *http.Cookie
	configPath      string
	oldPasswordHash string
}

func newResetTestFixture(t *testing.T) resetTestFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.SetupReady = true
	cfg.Web.Hostname = "scert.example.test"
	cfg.DNS.VerificationState = "verified"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.test"
	oldPasswordHash, err := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(oldPasswordHash)
	configPath := filepath.Join(dir, "config", "scert.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	controller := admin.New(cfg)
	session, err := controller.Start()
	if err != nil {
		t.Fatalf("启动测试重置会话失败：%v", err)
	}
	server := newLegacyResetServer(Dependencies{Config: cfg, ConfigPath: configPath, Repository: &stubDomainRepository{}, ResetStatePath: admin.StatePath(cfg.Paths.Data)})
	get := httptest.NewRecorder()
	serveAdminHTTP(server, get, httptest.NewRequest(http.MethodGet, "https://scert.example.test/reset", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("获取测试重置页面失败：%d", get.Code)
	}
	cookies := get.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("测试重置页面应返回一个 CSRF Cookie：%d", len(cookies))
	}
	return resetTestFixture{
		server: server, cfg: cfg, controller: controller, session: session,
		cookie: cookies[0], configPath: configPath, oldPasswordHash: cfg.Admin.PasswordHash,
	}
}

func resetForm(code, csrfToken, password string) url.Values {
	return url.Values{
		"csrf_token":       {csrfToken},
		"reset_code":       {code},
		"new_password":     {password},
		"confirm_password": {password},
	}
}

func TestListenAddressKeepsDirectModeBinding(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 5200
	cfg.Server.HTTPSMode = "direct"
	address, err := listenAddress(cfg)
	if err != nil {
		t.Fatalf("直接 HTTPS 模式监听地址生成失败：%v", err)
	}
	if address != "0.0.0.0:5200" {
		t.Fatalf("直接 HTTPS 模式应保留配置监听地址：%s", address)
	}
}

func TestListenAddressRestrictsProxyModeToLoopback(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 5201
	cfg.Server.HTTPSMode = "proxy"
	address, err := listenAddress(cfg)
	if err != nil {
		t.Fatalf("高级 HTTPS 模式监听地址生成失败：%v", err)
	}
	if address != "127.0.0.1:5201" {
		t.Fatalf("高级 HTTPS 模式必须绑定本机回环地址：%s", address)
	}

	cfg.Server.Host = "192.168.2.3"
	address, err = listenAddress(cfg)
	if err != nil {
		t.Fatalf("高级 HTTPS 模式公网地址校正失败：%v", err)
	}
	if address != "127.0.0.1:5201" {
		t.Fatalf("高级 HTTPS 模式不得绑定局域网地址：%s", address)
	}
}

func TestListenAddressAllowsLoopbackProxyBinding(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.Server.Host = "[::1]"
	cfg.Server.Port = 5202
	cfg.Server.HTTPSMode = "proxy"
	address, err := listenAddress(cfg)
	if err != nil {
		t.Fatalf("回环 HTTPS 模式监听地址生成失败：%v", err)
	}
	if address != "[::1]:5202" {
		t.Fatalf("已配置 IPv6 回环地址时不应被替换：%s", address)
	}
}

func TestForwardedHTTPSIsTrustedOnlyInProxyMode(t *testing.T) {
	direct := readyTestConfig(t.TempDir())
	direct.Server.HTTPSMode = "direct"
	directServer := New(Dependencies{Config: direct, Repository: &stubDomainRepository{}})
	directRequest := httptest.NewRequest(http.MethodGet, "http://scert.example.test/healthz", nil)
	directRequest.Header.Set("X-Forwarded-Proto", "https")
	if directServer.requestIsHTTPS(directRequest) {
		t.Fatal("直接 HTTPS 模式不应信任外部伪造的 X-Forwarded-Proto")
	}

	proxy := readyTestConfig(t.TempDir())
	proxy.Server.HTTPSMode = "proxy"
	proxyServer := New(Dependencies{Config: proxy, Repository: &stubDomainRepository{}})
	proxyRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5200/healthz", nil)
	proxyRequest.Header.Set("X-Forwarded-Proto", "https, http")
	if !proxyServer.requestIsHTTPS(proxyRequest) {
		t.Fatal("高级 HTTPS 模式应识别可信反向代理传入的 HTTPS 协议")
	}
}

func TestProxyModeAcceptsHTTPSSameOrigin(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	cfg.SetupReady = true
	cfg.Server.HTTPSMode = "proxy"
	cfg.Admin.Username = "admin"
	cfg.Admin.Email = "admin@example.com"
	hash, err := bcrypt.GenerateFromPassword([]byte("Password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成测试密码摘要失败：%v", err)
	}
	cfg.Admin.PasswordHash = string(hash)
	server := New(Dependencies{Config: cfg, Repository: &stubDomainRepository{items: []domain.Domain{}}})

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5200/api/v1/domains", nil)
	request.Host = "scert.example.test"
	request.Header.Set("Origin", "https://scert.example.test")
	request.Header.Set("X-Forwarded-Proto", "https")
	if !server.sameOriginForAPI(request) {
		t.Fatal("高级 HTTPS 模式应接受反向代理后的同源 HTTPS 请求")
	}
}

func TestDomainAPIReportsMissingRepository(t *testing.T) {
	cfg := readyTestConfig(t.TempDir())
	server := New(Dependencies{Config: cfg})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil)
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)

	if response.Code != http.StatusNotImplemented {
		t.Fatalf("域名仓储未配置时应返回 501：%d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "域名管理接口尚未配置") {
		t.Fatalf("缺少域名仓储错误提示：%s", response.Body.String())
	}
}

func TestDeleteDomainRejectsActiveCertificateTask(t *testing.T) {
	dir := t.TempDir()
	cfg := readyTestConfig(dir)
	cfg.Database.DSN = filepath.Join(dir, "data", "delete-domain.db")
	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("数据库迁移失败：%v", err)
	}

	domainRepo := repository.NewSQLDomainRepository(db.DB)
	taskRepo := repository.NewSQLTaskRepository(db.DB)
	server := New(Dependencies{
		Config:     cfg,
		Repository: domainRepo,
		Tasks:      taskRepo,
	})

	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"name":"active-task.example.com","certificate_type":"single"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	serveAdminHTTP(server, createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("创建测试域名失败：%d %s", createResponse.Code, createResponse.Body.String())
	}

	var createdDomain domain.Domain
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createdDomain); err != nil {
		t.Fatalf("解析创建域名响应失败：%v", err)
	}
	if createdDomain.ID == "" {
		t.Fatal("创建域名响应缺少域名 ID")
	}

	now := time.Now().UTC()
	if err := taskRepo.CreateTask(context.Background(), repository.Task{
		ID:        "active-certificate-task",
		DomainID:  createdDomain.ID,
		TaskType:  "certificate_issue",
		Status:    "running",
		Stage:     "certificate_issue",
		Message:   "正在执行",
		StartedAt: &now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("创建活动证书任务失败：%v", err)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/domains/"+createdDomain.ID, nil)
	blockedResponse := httptest.NewRecorder()
	serveAdminHTTP(server, blockedResponse, deleteRequest)
	if blockedResponse.Code != http.StatusConflict {
		t.Fatalf("活动证书任务期间删除域名应返回 409：%d %s", blockedResponse.Code, blockedResponse.Body.String())
	}
	if !strings.Contains(blockedResponse.Body.String(), "正在执行的证书任务") {
		t.Fatalf("活动证书任务期间的错误信息不正确：%s", blockedResponse.Body.String())
	}

	finishedAt := now.Add(time.Second)
	if err := taskRepo.CancelTask(context.Background(), "active-certificate-task", "测试取消", &finishedAt); err != nil {
		t.Fatalf("取消活动证书任务失败：%v", err)
	}

	allowedResponse := httptest.NewRecorder()
	serveAdminHTTP(server, allowedResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/domains/"+createdDomain.ID, nil))
	if allowedResponse.Code != http.StatusNoContent {
		t.Fatalf("取消证书任务后删除域名应返回 204：%d %s", allowedResponse.Code, allowedResponse.Body.String())
	}
	if _, err := domainRepo.GetByID(context.Background(), createdDomain.ID); !repository.IsNotFound(err) {
		t.Fatalf("删除成功后仍能读取域名：%v", err)
	}
}
