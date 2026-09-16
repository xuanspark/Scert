package service

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesUninitializedRuntime(t *testing.T) {
	baseDir := t.TempDir()
	runtime, err := Open(filepath.Join(baseDir, "config", "scert.json"), baseDir)
	if err != nil {
		t.Fatalf("打开桌面核心服务失败：%v", err)
	}
	defer runtime.Close()
	if runtime.core != nil {
		t.Fatal("桌面首次初始化不应创建正式应用")
	}
	if _, err := os.Stat(defaultConfig(baseDir).Database.DSN); !os.IsNotExist(err) {
		t.Fatalf("桌面初始化提前创建数据库：%v", err)
	}

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/setup/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.RemoteAddr = "127.0.0.1:1234"
	response := newRecordingResponse()
	runtime.Handler().ServeHTTP(response, request)
	if response.status != http.StatusOK {
		t.Fatalf("初始化状态接口状态码错误：%d，响应：%s", response.status, response.body)
	}
}

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, "config", "scert.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(configPath, baseDir); err == nil {
		t.Fatal("无效配置不应打开成功")
	}
}

type recordingResponse struct {
	header http.Header
	status int
	body   string
}

func newRecordingResponse() *recordingResponse {
	return &recordingResponse{header: make(http.Header)}
}

func (r *recordingResponse) Header() http.Header {
	return r.header
}

func (r *recordingResponse) WriteHeader(status int) {
	r.status = status
}

func (r *recordingResponse) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body += string(body)
	return len(body), nil
}

func TestOpenAppliesEnvironmentOverrides(t *testing.T) {
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, "config", "scert.json")
	cfg := defaultConfig(baseDir)
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	t.Setenv("SCERT_WEB_ENABLED", "false")
	runtime, err := Open(configPath, baseDir)
	if err != nil {
		t.Fatalf("应用环境覆盖后打开桌面核心服务失败：%v", err)
	}
	defer runtime.Close()

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.RemoteAddr = "127.0.0.1:1234"
	response := newRecordingResponse()
	runtime.Handler().ServeHTTP(response, request)
	if response.status != http.StatusNotFound {
		t.Fatalf("SCERT_WEB_ENABLED=false 未关闭 Web 页面，状态码：%d", response.status)
	}
}

func TestOpenRejectsInvalidEnvironmentOverride(t *testing.T) {
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, "config", "scert.json")
	if err := defaultConfig(baseDir).Save(configPath); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	t.Setenv("SCERT_WEB_ENABLED", "无效")
	if _, err := Open(configPath, baseDir); err == nil {
		t.Fatal("无效环境覆盖不应打开桌面核心服务")
	}
}
