package bootstrap

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/dnsprovider"
	"git.pcux.cn/xuans/Scert/backend/internal/firewall"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/setup"
)

type ServerOptions struct {
	Config       config.Config
	ConfigPath   string
	PublicIP     string
	URLPath      string
	Output       io.Writer
	FirewallMode string
}

// Run 只创建初始化所需组件；正式数据库、业务路由和后台任务由切换后的应用创建。
func Run(ctx context.Context, options ServerOptions) (bool, error) {
	if options.Config.SetupReady {
		return false, errors.New("已经初始化，不能启动临时初始化服务")
	}
	firewallManager, err := firewall.NewManagerWithMode(options.FirewallMode)
	if err != nil {
		return false, err
	}
	material, err := NewTemporaryTLSMaterial(options.PublicIP)
	if err != nil {
		return false, err
	}
	sessions, token, err := NewSessionManager()
	if err != nil {
		return false, err
	}
	defer sessions.RevokeAll()
	ip, err := advertisedBootstrapIP(options.PublicIP, material.IPAddresses)
	if err != nil {
		return false, err
	}
	address, err := bootstrapURL(ip, options.Config.Server.Port, token)
	if err != nil {
		return false, err
	}
	store, err := secretstore.Open(options.Config.Paths.Config)
	if err != nil {
		return false, fmt.Errorf("打开初始化敏感信息存储失败：%w", err)
	}
	databases := NewDatabaseVerificationManager(options.Config, store)
	dns := setup.NewDNSVerificationManager(dnsprovider.NewTester(dnsprovider.NewLegoFactory()), store)
	// 服务完全停止后再清理未领取的凭据，避免在途验证重新留下临时凭据。
	defer databases.DiscardAllExcept()
	defer dns.DiscardAll()
	issuer := certificate.NewService(options.Config, nil, store, dnsprovider.NewLegoFactory(), certificate.NewAccountRepository(store))
	issuer.SetDNSAuthorizationChecker(dnsprovider.NewAliyunAuthorizationChecker())
	completer := NewCompleter(CompleteDependencies{
		BaseConfig: options.Config, ConfigPath: options.ConfigPath, BootstrapURLPath: options.URLPath,
		DatabaseSessions: databases, DNSSessions: dns, Credentials: store, Sessions: sessions,
		Firewall: firewallManager, Certificate: issuer,
		Warn: func(message string) {
			if options.Output != nil {
				fmt.Fprintln(options.Output, message)
			}
		},
	})
	router := NewRouter(sessions, databases, dns, completer)
	handler, closeFiles := bootstrapHandler(router, options.Config)
	defer closeFiles()
	listener, err := net.Listen("tcp", net.JoinHostPort(options.Config.Server.Host, strconv.Itoa(options.Config.Server.Port)))
	if err != nil {
		return false, fmt.Errorf("监听初始化端口失败：%w", err)
	}
	defer listener.Close()
	if err := WriteBootstrapURL(options.URLPath, address); err != nil {
		return false, err
	}
	defer func() {
		if err := os.Remove(options.URLPath); err != nil && !errors.Is(err, os.ErrNotExist) && options.Output != nil {
			fmt.Fprintln(options.Output, "初始化地址文件清理失败，请检查运行目录权限。")
		}
	}()
	if options.Output != nil {
		writeBootstrapNotice(options.Output, options.Config.Server.Port, options.URLPath, material)
		if strings.EqualFold(strings.TrimSpace(options.FirewallMode), firewall.ModeExternal) {
			fmt.Fprintln(options.Output, "初始化防火墙采用 external 模式：不检查或修改主机规则及云安全组，请管理员预先放行所需端口。")
		}
	}
	return serveTemporaryHTTPS(ctx, listener, material, handler, router.Completed())
}

func writeBootstrapNotice(output io.Writer, port int, path string, material TLSMaterial) {
	// 指纹是公开证书摘要；一次性令牌和私钥仍只能留在受保护文件或内存中。
	fingerprint := sha256.Sum256(material.Certificate.Certificate[0])
	fmt.Fprintf(output, "首次初始化 HTTPS 服务已启动，端口 %d。请读取 %s 获取一次性访问地址。\n", port, path)
	fmt.Fprintf(output, "Bootstrap 临时证书 SHA-256 指纹：%X\n请通过可信运维通道与浏览器证书详情核对；指纹不一致时不要输入初始化令牌、密码或 RAM 凭据。\n", fingerprint)
}

func serveTemporaryHTTPS(ctx context.Context, listener net.Listener, material TLSMaterial, handler http.Handler, completed <-chan struct{}) (bool, error) {
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	var active sync.WaitGroup
	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			active.Add(1)
			defer active.Done()
			handler.ServeHTTP(writer, request)
		}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024,
		TLSConfig:   &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{material.Certificate}},
		BaseContext: func(net.Listener) context.Context { return requestContext },
	}
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- server.ServeTLS(listener, "", "") }()
	success := false
	var serveErr error
	select {
	case <-completed:
		success = true
	case <-ctx.Done():
		cancelRequests()
	case serveErr = <-errorsChannel:
		cancelRequests()
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdownContext)
	if shutdownErr != nil {
		cancelRequests()
		_ = server.Close()
	}
	// Shutdown 已停止接收新请求；等待在途处理完成后才能清理其凭据。
	active.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return false, serveErr
	}
	if shutdownErr != nil {
		return false, fmt.Errorf("关闭初始化服务失败：%w", shutdownErr)
	}
	return success, nil
}

func bootstrapHandler(router *Router, cfg config.Config) (http.Handler, func()) {
	root, _ := os.OpenRoot(filepath.Join(cfg.Paths.Install, "web"))
	closeFiles := func() {
		if root != nil {
			_ = root.Close()
		}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		setBootstrapSecurityHeaders(writer)
		if request.URL.Path == "/api/v1/bootstrap/settings" {
			if !requireBootstrapMethod(writer, request, http.MethodGet) || !router.authenticate(writer, request) {
				return
			}
			writeBootstrapJSON(writer, http.StatusOK, map[string]any{
				"mode": "bootstrap", "initialized": false, "server_port": cfg.Server.Port,
				"web_enabled": true, "web_hostname": cfg.Web.Hostname, "web_public_ip": cfg.Web.PublicIP,
				"https_mode": cfg.Server.HTTPSMode, "certificate_root": cfg.Paths.Certificate,
				"dns_provider": dnsprovider.Aliyun, "acme_environment": cfg.ACME.Environment,
				"propagation_timeout_secs":          cfg.ACME.PropagationTimeoutSecs,
				"propagation_polling_interval_secs": cfg.ACME.PropagationPollingIntervalSecs,
			})
			return
		}
		path := request.URL.Path
		if strings.HasPrefix(path, "/api/") {
			router.ServeHTTP(writer, request)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(writer, request)
			return
		}
		name := strings.TrimPrefix(path, "/")
		if path == "/" {
			name = "index.html"
		} else if path != "/Scert.png" && path != "/favicon.ico" && !strings.HasPrefix(path, "/assets/") {
			http.NotFound(writer, request)
			return
		}
		if !fs.ValidPath(name) {
			writeBootstrapError(writer, http.StatusBadRequest, "资源路径无效")
			return
		}
		if root == nil {
			http.Error(writer, "初始化页面资源尚未安装，请检查 web 目录。", http.StatusServiceUnavailable)
			return
		}
		// os.Root 限制符号链接逃逸，不暴露配置目录或任意业务文件。
		file, err := root.Open(name)
		if err != nil {
			writeBootstrapError(writer, http.StatusNotFound, "资源不存在")
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			writeBootstrapError(writer, http.StatusNotFound, "资源不存在")
			return
		}
		http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
	}), closeFiles
}
