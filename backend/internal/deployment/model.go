package deployment

import (
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
	"time"
)

type WebServer string

const (
	WebServerNginx  WebServer = "nginx"
	WebServerCaddy  WebServer = "caddy"
	WebServerApache WebServer = "apache"
	WebServerCustom WebServer = "custom"
)

const (
	DefaultCertificatePath      = "/ssl/{域名}/{证书类型}/cert.pem"
	DefaultCertificateChainPath = "/ssl/{域名}/{证书类型}/fullchain.pem"
	DefaultPrivateKeyPath       = "/ssl/{域名}/{证书类型}/key.pem"
)

var serverPathPlaceholderPattern = regexp.MustCompile(`\{[^{}]+\}`)

type Server struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Address              string    `json:"address"`
	Port                 int       `json:"port"`
	Username             string    `json:"username"`
	PrivateKeyRef        string    `json:"private_key_ref"`
	HostKeyFingerprint   string    `json:"host_key_fingerprint"`
	CertificatePath      string    `json:"certificate_path"`
	PrivateKeyPath       string    `json:"private_key_path"`
	CertificateChainPath string    `json:"certificate_chain_path"`
	WebServer            WebServer `json:"web_server"`
	ReloadCommand        []string  `json:"reload_command,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (s Server) Normalize() Server {
	s.ID = strings.TrimSpace(s.ID)
	s.Name = strings.TrimSpace(s.Name)
	s.Address = strings.TrimSpace(s.Address)
	s.Username = strings.TrimSpace(s.Username)
	s.PrivateKeyRef = strings.TrimSpace(s.PrivateKeyRef)
	s.HostKeyFingerprint = strings.TrimSpace(s.HostKeyFingerprint)
	s.CertificatePath = strings.TrimSpace(s.CertificatePath)
	s.PrivateKeyPath = strings.TrimSpace(s.PrivateKeyPath)
	s.CertificateChainPath = strings.TrimSpace(s.CertificateChainPath)
	if s.CertificatePath == "" {
		s.CertificatePath = DefaultCertificatePath
	}
	if s.CertificateChainPath == "" {
		s.CertificateChainPath = DefaultCertificateChainPath
	}
	if s.PrivateKeyPath == "" {
		s.PrivateKeyPath = DefaultPrivateKeyPath
	}
	s.WebServer = WebServer(strings.ToLower(strings.TrimSpace(string(s.WebServer))))
	if s.Port == 0 {
		s.Port = 22
	}
	if len(s.ReloadCommand) == 0 {
		s.ReloadCommand = DefaultReloadCommand(s.WebServer)
	}
	for index := range s.ReloadCommand {
		s.ReloadCommand[index] = strings.TrimSpace(s.ReloadCommand[index])
	}
	return s
}

func ResolveServerPaths(server Server, domainName, certificateType string) (Server, error) {
	server = server.Normalize()
	domainDirectory, err := normalizeDomainDirectory(domainName)
	if err != nil {
		return Server{}, err
	}
	certificateType = strings.ToLower(strings.TrimSpace(certificateType))
	switch certificateType {
	case "single", "wildcard", "san":
	default:
		return Server{}, fmt.Errorf("证书类型无法用于远程路径：%s", certificateType)
	}
	replacer := strings.NewReplacer(
		"{域名}", domainDirectory,
		"{domain}", domainDirectory,
		"{证书类型}", certificateType,
		"{certificate_type}", certificateType,
	)
	server.CertificatePath, err = resolveServerPathTemplate("远程证书路径", server.CertificatePath, replacer)
	if err != nil {
		return Server{}, err
	}
	server.CertificateChainPath, err = resolveServerPathTemplate("远程完整证书链路径", server.CertificateChainPath, replacer)
	if err != nil {
		return Server{}, err
	}
	server.PrivateKeyPath, err = resolveServerPathTemplate("远程私钥路径", server.PrivateKeyPath, replacer)
	if err != nil {
		return Server{}, err
	}
	return server, nil
}

func normalizeDomainDirectory(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "*.")
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00\r\n") {
		return "", fmt.Errorf("域名无法用于远程路径：%s", value)
	}
	return value, nil
}

func resolveServerPathTemplate(label, value string, replacer *strings.Replacer) (string, error) {
	resolved := replacer.Replace(strings.TrimSpace(value))
	if placeholder := serverPathPlaceholderPattern.FindString(resolved); placeholder != "" {
		return "", fmt.Errorf("%s包含不支持的占位符：%s", label, placeholder)
	}
	cleaned := RemotePath(resolved)
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("%s无效", label)
	}
	if !strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%s必须是绝对路径", label)
	}
	return resolved, nil
}

func (s Server) Validate() error {
	s = s.Normalize()
	if s.ID == "" {
		return errors.New("服务器 ID 不能为空")
	}
	if s.Name == "" {
		return errors.New("服务器名称不能为空")
	}
	if s.Address == "" || net.ParseIP(s.Address) == nil && strings.ContainsAny(s.Address, " /\\") {
		return errors.New("服务器地址无效")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("SSH 端口无效")
	}
	if s.Username == "" {
		return errors.New("SSH 用户不能为空")
	}
	if s.PrivateKeyRef == "" {
		return errors.New("SSH 私钥引用不能为空")
	}
	if s.HostKeyFingerprint == "" {
		return errors.New("SSH 主机指纹不能为空")
	}
	if s.CertificatePath == "" || s.PrivateKeyPath == "" || s.CertificateChainPath == "" {
		return errors.New("远程证书路径不能为空")
	}
	if _, err := ResolveServerPaths(s, "example.com", "single"); err != nil {
		return err
	}
	if s.WebServer != WebServerNginx && s.WebServer != WebServerCaddy && s.WebServer != WebServerApache && s.WebServer != WebServerCustom {
		return fmt.Errorf("网站服务类型无效：%s", s.WebServer)
	}
	if len(s.ReloadCommand) == 0 || s.ReloadCommand[0] == "" {
		return errors.New("reload 命令不能为空")
	}
	for _, argument := range s.ReloadCommand {
		if argument == "" || strings.ContainsAny(argument, "\x00\r\n") {
			return errors.New("reload 命令参数无效")
		}
	}
	return nil
}

func DefaultReloadCommand(webServer WebServer) []string {
	switch WebServer(strings.ToLower(strings.TrimSpace(string(webServer)))) {
	case WebServerNginx:
		return []string{"nginx", "-s", "reload"}
	case WebServerCaddy:
		return []string{"caddy", "reload"}
	case WebServerApache:
		return []string{"apachectl", "-k", "graceful"}
	default:
		return nil
	}
}

func RemotePath(value string) string {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return path.Clean(strings.ReplaceAll(value, "\\", "/"))
}
