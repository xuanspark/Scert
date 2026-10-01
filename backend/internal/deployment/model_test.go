package deployment

import "testing"

func TestServerUsesDefaultReloadCommand(t *testing.T) {
	server := testServer("one").Normalize()
	if err := server.Validate(); err != nil {
		t.Fatalf("服务器配置校验失败：%v", err)
	}
	if len(server.ReloadCommand) != 3 || server.ReloadCommand[0] != "nginx" {
		t.Fatalf("Nginx 默认 reload 命令错误：%v", server.ReloadCommand)
	}
	if len(server.ValidationCommand) != 2 || server.ValidationCommand[0] != "nginx" || server.ValidationCommand[1] != "-t" {
		t.Fatalf("Nginx 默认配置校验命令错误：%v", server.ValidationCommand)
	}
}

func TestServerRequiresCustomValidationCommand(t *testing.T) {
	server := testServer("one")
	server.WebServer = WebServerCustom
	server.ValidationCommand = nil
	server.ReloadCommand = []string{"service-control", "reload"}
	if err := server.Validate(); err == nil {
		t.Fatal("自定义网站服务未配置校验命令时应被拒绝")
	}
	server.ValidationCommand = []string{"service-control", "validate"}
	if err := server.Validate(); err != nil {
		t.Fatalf("自定义网站服务配置有效命令后应通过校验：%v", err)
	}
}

func TestServerUsesDefaultCertificatePathTemplates(t *testing.T) {
	server := testServer("one")
	server.CertificatePath = ""
	server.CertificateChainPath = ""
	server.PrivateKeyPath = ""
	server = server.Normalize()
	if server.CertificatePath != DefaultCertificatePath || server.CertificateChainPath != DefaultCertificateChainPath || server.PrivateKeyPath != DefaultPrivateKeyPath {
		t.Fatalf("远程证书默认路径模板错误：%+v", server)
	}
}

func TestResolveServerPathsByCertificateType(t *testing.T) {
	tests := []struct {
		name            string
		domainName      string
		certificateType string
		expectedRoot    string
	}{
		{name: "单域名", domainName: "example.com", certificateType: "single", expectedRoot: "/ssl/example.com/single"},
		{name: "泛域名", domainName: "*.example.com", certificateType: "wildcard", expectedRoot: "/ssl/example.com/wildcard"},
		{name: "SAN", domainName: "example.com", certificateType: "san", expectedRoot: "/ssl/example.com/san"},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			server := testServer("one")
			server.CertificatePath = ""
			server.CertificateChainPath = ""
			server.PrivateKeyPath = ""
			resolved, err := ResolveServerPaths(server, item.domainName, item.certificateType)
			if err != nil {
				t.Fatalf("解析远程证书路径失败：%v", err)
			}
			if resolved.CertificatePath != item.expectedRoot+"/cert.pem" || resolved.CertificateChainPath != item.expectedRoot+"/fullchain.pem" || resolved.PrivateKeyPath != item.expectedRoot+"/key.pem" {
				t.Fatalf("远程证书路径解析错误：%+v", resolved)
			}
		})
	}
}

func TestResolveServerPathsKeepsCustomPathAndRejectsUnknownPlaceholder(t *testing.T) {
	server := testServer("one")
	server.CertificatePath = "/custom/{domain}/{certificate_type}/certificate.crt"
	resolved, err := ResolveServerPaths(server, "example.com", "single")
	if err != nil {
		t.Fatalf("解析自定义远程证书路径失败：%v", err)
	}
	if resolved.CertificatePath != "/custom/example.com/single/certificate.crt" {
		t.Fatalf("自定义远程证书路径被错误处理：%s", resolved.CertificatePath)
	}
	server.CertificatePath = "/ssl/{未知占位符}/cert.pem"
	if _, err := ResolveServerPaths(server, "example.com", "single"); err == nil {
		t.Fatal("包含未知占位符的远程证书路径应被拒绝")
	}
}

func TestResolveServerPathsRejectsRelativePath(t *testing.T) {
	server := testServer("one")
	server.CertificatePath = "cert.pem"
	if _, err := ResolveServerPaths(server, "example.com", "single"); err == nil {
		t.Fatal("相对远程证书路径应被拒绝")
	}
}

func TestResolveServerPathsCanonicalizesPathAndRejectsDuplicate(t *testing.T) {
	server := testServer("one")
	server.CertificatePath = "/ssl/example/../example/cert.pem"
	resolved, err := ResolveServerPaths(server, "example.com", "single")
	if err != nil {
		t.Fatalf("path normalization failed: %v", err)
	}
	if resolved.CertificatePath != "/ssl/example/cert.pem" {
		t.Fatalf("unexpected normalized path: %s", resolved.CertificatePath)
	}

	server = testServer("one")
	server.CertificatePath = "/ssl/example/../example/cert.pem"
	server.PrivateKeyPath = "/ssl/example/../example/cert.pem"
	if _, err := ResolveServerPaths(server, "example.com", "single"); err == nil {
		FatalUnexpectedPath(t)
	}
}
func FatalUnexpectedPath(t *testing.T) {
	t.Helper()
	t.Fatal("duplicate path accepted")
}
func TestServerRejectsPasswordStyleOrUnsafeConfiguration(t *testing.T) {
	server := testServer("one")
	server.PasswordRef = ""
	if err := server.Validate(); err == nil {
		t.Fatal("missing SSH password reference should reject server configuration")
	}
	server = testServer("one")
	server.PrivateKeyRef = "secret://ssh/private-key"
	if err := server.Validate(); err == nil {
		t.Fatal("SSH private key reference should reject server configuration")
	}
	server = testServer("one")
	server.ReloadCommand = []string{"nginx\nrm", "-s", "reload"}
	if err := server.Validate(); err == nil {
		t.Fatal("reload command containing a newline should be rejected")
	}
	server = testServer("one")
	server.ValidationCommand = []string{"nginx", "-t\nrm"}
	if err := server.Validate(); err == nil {
		t.Fatal("validation command containing a newline should be rejected")
	}
}
