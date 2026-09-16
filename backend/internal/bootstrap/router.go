package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

const (
	bootstrapSessionRequestLimit   = 4 * 1024
	bootstrapRequestLimit          = 64 * 1024
	databaseVerificationTimeout    = 20 * time.Second
	bootstrapCompletionTimeout     = 12 * time.Minute
	bootstrapDatabaseDisplaySQLite = "SQLite"
)

type bootstrapDatabaseVerifier interface {
	Verify(context.Context, DatabaseInput) (DatabaseVerificationResult, error)
}

type bootstrapDNSVerifier interface {
	Verify(context.Context, setup.DNSVerificationInput) (setup.DNSVerificationResult, error)
}

type bootstrapCompleter interface {
	Complete(context.Context, CompleteRequest) (CompleteResult, error)
}

// Router 只承载首次初始化接口，不注册正式管理接口。
type Router struct {
	sessions     *SessionManager
	database     bootstrapDatabaseVerifier
	dns          bootstrapDNSVerifier
	completer    bootstrapCompleter
	completed    chan struct{}
	completeOnce sync.Once
}

type bootstrapSessionRequest struct {
	Token string `json:"token"`
}

type bootstrapDNSTestRequest struct {
	Provider    string            `json:"provider"`
	TestDomain  string            `json:"test_domain"`
	Credentials map[string]string `json:"credentials"`
}

// NewRouter 创建与正式管理服务完全分离的首次初始化路由。
func NewRouter(sessions *SessionManager, database bootstrapDatabaseVerifier, dns bootstrapDNSVerifier, completer bootstrapCompleter) *Router {
	return &Router{sessions: sessions, database: database, dns: dns, completer: completer, completed: make(chan struct{})}
}

// Completed 在成功响应写入后通知临时服务开始优雅关闭。
func (router *Router) Completed() <-chan struct{} { return router.completed }

func (router *Router) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setBootstrapSecurityHeaders(writer)
	if request == nil {
		writeBootstrapError(writer, http.StatusBadRequest, "请求无效")
		return
	}
	switch request.URL.Path {
	case "/api/v1/bootstrap/status":
		if !requireBootstrapMethod(writer, request, http.MethodGet) {
			return
		}
		router.status(writer)
	case "/api/v1/bootstrap/session":
		if !requireBootstrapMethod(writer, request, http.MethodPost) {
			return
		}
		router.exchangeSession(writer, request)
	case "/api/v1/bootstrap/database/test":
		if !requireBootstrapMethod(writer, request, http.MethodPost) || !router.authenticate(writer, request) {
			return
		}
		router.testDatabase(writer, request)
	case "/api/v1/bootstrap/dns/providers":
		if !requireBootstrapMethod(writer, request, http.MethodGet) || !router.authenticate(writer, request) {
			return
		}
		router.listDNSProviders(writer)
	case "/api/v1/bootstrap/dns/test":
		if !requireBootstrapMethod(writer, request, http.MethodPost) || !router.authenticate(writer, request) {
			return
		}
		router.testDNS(writer, request)
	case "/api/v1/bootstrap/complete":
		if !requireBootstrapMethod(writer, request, http.MethodPost) || !router.authenticate(writer, request) {
			return
		}
		router.complete(writer, request)
	default:
		writeBootstrapError(writer, http.StatusNotFound, "接口不存在")
	}
}

func (router *Router) status(writer http.ResponseWriter) {
	writeBootstrapJSON(writer, http.StatusOK, map[string]any{
		"mode":                    "bootstrap",
		"session_required":        true,
		"database_test_available": router != nil && router.database != nil,
		"dns_test_available":      router != nil && router.dns != nil,
		"complete_available":      router != nil && router.completer != nil,
		"database_drivers": []map[string]string{
			{"id": "sqlite", "name": bootstrapDatabaseDisplaySQLite},
			{"id": "mysql", "name": "MySQL"},
			{"id": "postgres", "name": "PostgreSQL"},
		},
		"dns_provider": dnsprovider.Aliyun,
	})
}

func (router *Router) exchangeSession(writer http.ResponseWriter, request *http.Request) {
	if router == nil || router.sessions == nil {
		writeBootstrapError(writer, http.StatusServiceUnavailable, "初始化会话服务尚未就绪")
		return
	}
	var input bootstrapSessionRequest
	if err := decodeBootstrapJSON(writer, request, &input, bootstrapSessionRequestLimit); err != nil {
		writeBootstrapError(writer, http.StatusBadRequest, "初始化令牌请求无效")
		return
	}
	if strings.TrimSpace(input.Token) == "" || len(input.Token) > 1024 {
		writeBootstrapError(writer, http.StatusBadRequest, "初始化令牌格式无效")
		return
	}
	session, err := router.sessions.Exchange(input.Token, request.RemoteAddr)
	if err != nil {
		switch {
		case errors.Is(err, ErrBootstrapRateLimited):
			writeBootstrapError(writer, http.StatusTooManyRequests, "初始化验证尝试过多，请稍后重试")
		case errors.Is(err, ErrBootstrapBusy), errors.Is(err, ErrBootstrapSessionFull):
			writeBootstrapError(writer, http.StatusServiceUnavailable, "初始化会话服务繁忙，请稍后重试")
		default:
			writeBootstrapError(writer, http.StatusUnauthorized, "初始化令牌无效或已经失效")
		}
		return
	}
	http.SetCookie(writer, BootstrapSessionCookie(session.Token, session.ExpiresAt))
	writeBootstrapJSON(writer, http.StatusOK, map[string]any{
		"authenticated": true,
		"expires_at":    session.ExpiresAt,
	})
}

func (router *Router) testDatabase(writer http.ResponseWriter, request *http.Request) {
	if router == nil || router.database == nil {
		writeBootstrapError(writer, http.StatusServiceUnavailable, "数据库验证服务尚未就绪")
		return
	}
	var input DatabaseInput
	if err := decodeBootstrapJSON(writer, request, &input, bootstrapRequestLimit); err != nil {
		writeBootstrapError(writer, http.StatusBadRequest, "数据库测试请求参数无效")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), databaseVerificationTimeout)
	defer cancel()
	result, err := router.database.Verify(ctx, input)
	if err != nil {
		writeBootstrapError(writer, http.StatusUnprocessableEntity, "数据库连接或写权限验证失败，请检查连接参数、TLS 配置和账号权限")
		return
	}
	writeBootstrapJSON(writer, http.StatusOK, map[string]any{
		"verified":                  true,
		"driver":                    result.Config.Driver,
		"verification_token":        result.VerificationToken,
		"expires_at":                result.ExpiresAt,
		"verification_timeout_secs": int(databaseVerificationTimeout.Seconds()),
		"message":                   "数据库连接和写权限验证通过。",
	})
}

func (router *Router) listDNSProviders(writer http.ResponseWriter) {
	descriptor, err := dnsprovider.GetDescriptor(dnsprovider.Aliyun)
	if err != nil {
		writeBootstrapError(writer, http.StatusServiceUnavailable, "阿里云 DNS 配置尚未就绪")
		return
	}
	writeBootstrapJSON(writer, http.StatusOK, map[string]any{"items": []dnsprovider.Descriptor{descriptor}})
}

func (router *Router) testDNS(writer http.ResponseWriter, request *http.Request) {
	if router == nil || router.dns == nil {
		writeBootstrapError(writer, http.StatusServiceUnavailable, "DNS 在线验证服务尚未就绪")
		return
	}
	var input bootstrapDNSTestRequest
	if err := decodeBootstrapJSON(writer, request, &input, bootstrapRequestLimit); err != nil {
		writeBootstrapError(writer, http.StatusBadRequest, "DNS 测试请求参数无效")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(input.Provider), dnsprovider.Aliyun) {
		writeBootstrapError(writer, http.StatusBadRequest, "首次初始化仅支持阿里云 DNS")
		return
	}
	timeout := dnsprovider.PermissionVerificationTimeoutFor(dnsprovider.Aliyun)
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	result, err := router.dns.Verify(ctx, setup.DNSVerificationInput{
		ProviderID:  dnsprovider.Aliyun,
		TestDomain:  input.TestDomain,
		Credentials: input.Credentials,
	})
	if err != nil {
		report := redactDNSReport(result.TestReport, input.Credentials)
		var verificationErr *setup.DNSVerificationError
		if errors.As(err, &verificationErr) {
			report = redactDNSReport(verificationErr.Report, input.Credentials)
		}
		writeBootstrapJSON(writer, http.StatusUnprocessableEntity, map[string]any{
			"error":                     "阿里云 DNS 在线验证失败，请根据验证步骤检查凭据、域名和 RAM 权限",
			"test_report":               report,
			"verification_timeout_secs": int(timeout.Seconds()),
		})
		return
	}
	writeBootstrapJSON(writer, http.StatusOK, map[string]any{
		"verified":                  true,
		"verification_token":        result.VerificationToken,
		"expires_at":                result.ExpiresAt,
		"test_report":               redactDNSReport(result.TestReport, input.Credentials),
		"verification_timeout_secs": int(timeout.Seconds()),
		"message":                   "阿里云 DNS 创建、查询和清理验证通过。",
	})
}

func (router *Router) complete(writer http.ResponseWriter, request *http.Request) {
	if router == nil || router.completer == nil {
		writeBootstrapError(writer, http.StatusServiceUnavailable, "初始化提交服务尚未就绪")
		return
	}
	var input CompleteRequest
	if err := decodeBootstrapJSON(writer, request, &input, bootstrapRequestLimit); err != nil {
		writeBootstrapError(writer, http.StatusBadRequest, "初始化提交请求参数无效")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), bootstrapCompletionTimeout)
	defer cancel()
	result, err := router.completer.Complete(ctx, input)
	if err != nil {
		writeBootstrapCompletionError(writer, err)
		return
	}
	if !result.Completed || strings.TrimSpace(result.ManagementURL) == "" {
		writeBootstrapError(writer, http.StatusInternalServerError, "初始化提交结果无效，请检查服务状态")
		return
	}
	router.sessions.RevokeAll()
	http.SetCookie(writer, ClearBootstrapSessionCookie())
	writeBootstrapJSON(writer, http.StatusOK, result)
	router.completeOnce.Do(func() { close(router.completed) })
}

func writeBootstrapCompletionError(writer http.ResponseWriter, err error) {
	var completionErr *CompleteError
	switch {
	case errors.Is(err, ErrCompleteBusy):
		writeBootstrapError(writer, http.StatusConflict, "初始化正在执行，请等待当前请求完成")
	case errors.Is(err, ErrCompleteDone):
		writeBootstrapError(writer, http.StatusConflict, "初始化已经完成，请使用正式管理入口")
	case errors.As(err, &completionErr):
		// 不转发底层错误或可注入的说明，防止密码、凭据引用及验证令牌泄露。
		switch completionErr.Kind {
		case CompleteErrorInput:
			writeBootstrapError(writer, http.StatusBadRequest, "初始化参数无效，请检查管理员、管理入口和服务配置")
		case CompleteErrorVerification:
			writeBootstrapError(writer, http.StatusUnprocessableEntity, "数据库或 DNS 验证结果无效、过期或不匹配，请重新验证")
		case CompleteErrorDependency:
			writeBootstrapError(writer, http.StatusBadGateway, "初始化依赖操作失败，请检查数据库、证书签发和防火墙状态后重新验证")
		default:
			writeBootstrapError(writer, http.StatusInternalServerError, "初始化提交失败，请检查服务器状态后重新验证")
		}
	default:
		writeBootstrapError(writer, http.StatusInternalServerError, "初始化提交失败，请检查服务器状态后重新验证")
	}
}

func redactDNSReport(report setup.DNSVerificationReport, credentials map[string]string) setup.DNSVerificationReport {
	redacted := report
	redacted.RecordName = redactCredentialValues(redacted.RecordName, credentials)
	redacted.Stages = append([]setup.DNSVerificationStage(nil), report.Stages...)
	for index := range redacted.Stages {
		redacted.Stages[index].Name = redactCredentialValues(redacted.Stages[index].Name, credentials)
		redacted.Stages[index].Status = redactCredentialValues(redacted.Stages[index].Status, credentials)
		redacted.Stages[index].Message = redactCredentialValues(redacted.Stages[index].Message, credentials)
	}
	return redacted
}

func redactCredentialValues(value string, credentials map[string]string) string {
	for _, credential := range credentials {
		credential = strings.TrimSpace(credential)
		if credential != "" {
			value = strings.ReplaceAll(value, credential, "[已隐藏]")
		}
	}
	return value
}

func (router *Router) authenticate(writer http.ResponseWriter, request *http.Request) bool {
	if router == nil || router.sessions == nil {
		writeBootstrapError(writer, http.StatusServiceUnavailable, "初始化会话服务尚未就绪")
		return false
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || !router.sessions.Authenticate(cookie.Value) {
		http.SetCookie(writer, ClearBootstrapSessionCookie())
		writeBootstrapError(writer, http.StatusUnauthorized, "初始化会话无效或已经过期")
		return false
	}
	return true
}

func decodeBootstrapJSON(writer http.ResponseWriter, request *http.Request, target any, limit int64) error {
	if request == nil || request.Body == nil {
		return errors.New("请求体为空")
	}
	contentType := strings.TrimSpace(request.Header.Get("Content-Type"))
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return errors.New("请求体必须使用 application/json")
		}
	}
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	data, err := io.ReadAll(request.Body)
	if err != nil {
		return err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errors.New("请求体必须是 JSON 对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("请求体包含多段 JSON 数据")
		}
		return err
	}
	return nil
}

func requireBootstrapMethod(writer http.ResponseWriter, request *http.Request, method string) bool {
	if request.Method == method {
		return true
	}
	writer.Header().Set("Allow", method)
	writeBootstrapError(writer, http.StatusMethodNotAllowed, "请求方法不受支持")
	return false
}

func setBootstrapSecurityHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-Frame-Options", "DENY")
}

func writeBootstrapError(writer http.ResponseWriter, status int, message string) {
	writeBootstrapJSON(writer, status, map[string]string{"error": message})
}

func writeBootstrapJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
