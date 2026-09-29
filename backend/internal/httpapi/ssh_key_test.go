package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/repository"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
	"git.pcux.cn/xuans/Scert/backend/internal/sshkey"
)

func TestSSHKeyAPIHidesPrivateKeyAndBlocksDeletingUsedReference(t *testing.T) {
	directory := t.TempDir()
	cfg := readyTestConfig(directory)
	cfg.Database.DSN = filepath.Join(directory, "data", "ssh-keys.db")
	db, err := database.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	defer db.Close()
	if err := database.Migrate(context.Background(), db.DB, "sqlite"); err != nil {
		t.Fatalf("数据库迁移失败：%v", err)
	}
	store, err := secretstore.Open(filepath.Join(directory, "config"))
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	serverRepo := repository.NewSQLServerRepository(db.DB)
	server := New(Dependencies{
		Config: cfg, Repository: repository.NewSQLDomainRepository(db.DB), Servers: serverRepo,
		SSHKeys:          sshkey.NewService(store),
		ProjectResources: projectResourceReaderStub{servers: serverRepo},
		ProjectDomains:   projectDomainRepositoryStub{DomainRepository: repository.NewSQLDomainRepository(db.DB)},
	})

	privateKey := testSSHPrivateKey(t)
	createBody, err := json.Marshal(sshKeyRequest{Name: "生产私钥", PrivateKey: privateKey})
	if err != nil {
		t.Fatalf("编码 SSH 私钥请求失败：%v", err)
	}
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", strings.NewReader(string(createBody)))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	serveAdminHTTP(server, createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("创建 SSH 私钥失败：%d %s", createResponse.Code, createResponse.Body.String())
	}
	if strings.Contains(createResponse.Body.String(), "PRIVATE KEY") || strings.Contains(createResponse.Body.String(), "private_key") {
		t.Fatalf("SSH 私钥创建响应泄露了敏感内容：%s", createResponse.Body.String())
	}
	var key sshkey.Metadata
	if err := json.Unmarshal(createResponse.Body.Bytes(), &key); err != nil || key.ID == "" || key.Reference == "" {
		t.Fatalf("SSH 私钥创建响应无效：%v %+v", err, key)
	}

	listResponse := httptest.NewRecorder()
	serveAdminHTTP(server, listResponse, httptest.NewRequest(http.MethodGet, "/api/v1/ssh-keys", nil))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), key.Reference) || strings.Contains(listResponse.Body.String(), "PRIVATE KEY") {
		t.Fatalf("SSH 私钥列表响应无效或泄露敏感内容：%d %s", listResponse.Code, listResponse.Body.String())
	}

	serverBody := `{"name":"生产服务器","address":"192.0.2.60","port":22,"username":"deploy","private_key_ref":"` + key.Reference + `","host_key_fingerprint":"SHA256:test","certificate_path":"/etc/scert/cert.pem","private_key_path":"/etc/scert/key.pem","certificate_chain_path":"/etc/scert/fullchain.pem","web_server":"nginx"}`
	serverRequest := httptest.NewRequest(http.MethodPost, "/api/v1/servers", strings.NewReader(serverBody))
	serverRequest.Header.Set("Content-Type", "application/json")
	serverResponse := httptest.NewRecorder()
	serveAdminHTTP(server, serverResponse, serverRequest)
	if serverResponse.Code != http.StatusCreated {
		t.Fatalf("创建使用 SSH 私钥的服务器失败：%d %s", serverResponse.Code, serverResponse.Body.String())
	}
	var createdServer struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(serverResponse.Body.Bytes(), &createdServer); err != nil || createdServer.ID == "" {
		t.Fatalf("服务器创建响应无效：%v %s", err, serverResponse.Body.String())
	}

	deleteUsedResponse := httptest.NewRecorder()
	serveAdminHTTP(server, deleteUsedResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/ssh-keys/"+key.ID, nil))
	if deleteUsedResponse.Code != http.StatusConflict {
		t.Fatalf("仍被使用的 SSH 私钥应禁止删除：%d %s", deleteUsedResponse.Code, deleteUsedResponse.Body.String())
	}
	deleteServerResponse := httptest.NewRecorder()
	serveAdminHTTP(server, deleteServerResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/servers/"+createdServer.ID, nil))
	if deleteServerResponse.Code != http.StatusNoContent {
		t.Fatalf("删除测试服务器失败：%d %s", deleteServerResponse.Code, deleteServerResponse.Body.String())
	}
	deleteKeyResponse := httptest.NewRecorder()
	serveAdminHTTP(server, deleteKeyResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/ssh-keys/"+key.ID, nil))
	if deleteKeyResponse.Code != http.StatusNoContent {
		t.Fatalf("删除未使用的 SSH 私钥失败：%d %s", deleteKeyResponse.Code, deleteKeyResponse.Body.String())
	}
}

func TestSSHKeyAPIRejectsInvalidKeyWithoutEchoingIt(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), Repository: &stubDomainRepository{}, SSHKeys: sshkey.NewService(store)})
	secret := "invalid-private-key-sensitive-content"
	body, _ := json.Marshal(sshKeyRequest{Name: "错误私钥", PrivateKey: secret})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("无效 SSH 私钥响应不安全：%d %s", response.Code, response.Body.String())
	}
}

func TestSSHKeyAPIRejectsOversizedRequestBody(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("failed to open secret store: %v", err)
	}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), Repository: &stubDomainRepository{}, SSHKeys: sshkey.NewService(store)})
	body := `{"name":"oversized request","private_key":"` + strings.Repeat("a", maxSSHKeyRequestBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	serveAdminHTTP(server, response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized SSH key request should return 413: %d %s", response.Code, response.Body.String())
	}
}

func TestSSHKeyAPIRejectsOversizedPrivateKeyAndPassphrase(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("failed to open secret store: %v", err)
	}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), Repository: &stubDomainRepository{}, SSHKeys: sshkey.NewService(store)})
	requests := []sshKeyRequest{
		{Name: "oversized key", PrivateKey: strings.Repeat("a", sshkey.MaxPrivateKeyBytes+1)},
		{Name: "oversized passphrase", PrivateKey: testSSHPrivateKey(t), Passphrase: strings.Repeat("p", 4097)},
	}
	for _, input := range requests {
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("failed to encode limit test request: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		serveAdminHTTP(server, response, request)
		leaksPassphrase := input.Passphrase != "" && strings.Contains(response.Body.String(), input.Passphrase)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), input.PrivateKey) || leaksPassphrase {
			t.Fatalf("oversized input should be rejected safely: %d %s", response.Code, response.Body.String())
		}
	}
}

func testSSHPrivateKey(t *testing.T) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成测试 SSH 私钥失败：%v", err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("编码测试 SSH 私钥失败：%v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

func TestSSHKeyAPIRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	store, err := secretstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开敏感信息存储失败：%v", err)
	}
	server := New(Dependencies{Config: readyTestConfig(t.TempDir()), Repository: &stubDomainRepository{}, SSHKeys: sshkey.NewService(store)})
	privateKey := testSSHPrivateKey(t)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "未知字段",
			body: `{"name":"测试私钥","private_key":` + mustJSONString(t, privateKey) + `,"unexpected":true}`,
		},
		{
			name: "多段 JSON",
			body: `{"name":"测试私钥","private_key":` + mustJSONString(t, privateKey) + `}{"name":"第二个私钥","private_key":` + mustJSONString(t, privateKey) + `}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/ssh-keys", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			serveAdminHTTP(server, response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s 应返回 400，实际为 %d：%s", tt.name, response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), privateKey) || strings.Contains(response.Body.String(), "PRIVATE KEY") {
				t.Fatalf("%s 响应泄露了私钥：%s", tt.name, response.Body.String())
			}
		})
	}
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("编码 JSON 字符串失败：%v", err)
	}
	return string(encoded)
}
