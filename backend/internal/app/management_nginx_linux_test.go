package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/nginxactivation"
)

// 系统根证书缓存仅在子进程内加载测试 CA，不改生产依赖或主机信任库。
func TestApplicationNginxIntegration(t *testing.T) {
	binary := os.Getenv("SCERT_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("未指定隔离验收使用的 Nginx 可执行文件")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("测试 Nginx 路径必须是绝对路径")
	}
	if scenario := os.Getenv("SCERT_APP_NGINX_CHILD"); scenario != "" {
		runApplicationNginxScenario(t, binary, scenario)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"normal", "stage_response", "commit_response", "restart_staged", "restart_upstream"} {
		t.Run(scenario, func(t *testing.T) {
			f := newApplicationTLSFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestApplicationNginxIntegration$", "-test.v", "-test.timeout=90s")
			command.Env = append(os.Environ(),
				"SCERT_APP_NGINX_CHILD="+scenario, "SCERT_APP_NGINX_ROOT="+f.root,
				"SSL_CERT_FILE="+f.caPath, "SSL_CERT_DIR="+filepath.Join(f.root, "empty-roots"),
				"GODEBUG=x509usefallbackroots=0",
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("隔离应用与 Nginx 测试失败：%v\n%s", err, output)
			}
		})
	}
}

type applicationFaultProxy struct {
	nginxactivation.ProxyClient
	scenario string
	pending  bool
}

func (p *applicationFaultProxy) StageRequest(ctx context.Context, id string, chain, key []byte) (string, error) {
	result, err := p.ProxyClient.StageRequest(ctx, id, chain, key)
	if err != nil {
		return result, err
	}
	if p.scenario == "stage_response" || p.scenario == "restart_staged" {
		p.pending = p.scenario == "restart_staged"
		return "", errors.New("注入暂存应答丢失")
	}
	return result, nil
}

func (p *applicationFaultProxy) Commit(ctx context.Context, id string) error {
	if p.scenario == "restart_upstream" {
		p.pending = true
		return errors.New("注入上游持久化后辅助服务不可达")
	}
	if err := p.ProxyClient.Commit(ctx, id); err != nil {
		return err
	}
	if p.scenario == "commit_response" {
		return errors.New("注入提交应答丢失")
	}
	return nil
}

func (p *applicationFaultProxy) ResolveRequest(ctx context.Context, id string) (nginxactivation.State, error) {
	if p.pending {
		return nginxactivation.State{}, errors.New("注入恢复查询不可达，保留未完成事务")
	}
	return p.ProxyClient.ResolveRequest(ctx, id)
}

func runApplicationNginxScenario(t *testing.T, binary, scenario string) {
	f := loadApplicationTLSFixture(t, os.Getenv("SCERT_APP_NGINX_ROOT"))
	application, stop := f.start(t)
	f.seed(t, application)
	rotation := startApplicationNginx(t, f, binary)
	client := startApplicationActivationIPC(t, rotation)
	proxy := &applicationFaultProxy{ProxyClient: client, scenario: scenario}
	activator := coordinatedManagementActivator{
		server: application.HTTP, directory: filepath.Join(f.config.Paths.Data, "management-activation"), proxy: proxy,
	}
	state, err := application.HTTP.ManagementCertificate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := activator.Ready(ctx, state); err != nil {
		t.Fatal(err)
	}
	err = activator.Activate(ctx, state, f.candidate)
	want, leaf := "committed", f.newLeaf
	if scenario == "stage_response" || scenario == "restart_staged" {
		want, leaf = "rolled_back", f.oldLeaf
	}
	if scenario == "stage_response" || strings.HasPrefix(scenario, "restart_") {
		if err == nil {
			t.Fatal("注入故障后不应报告切换成功")
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(scenario, "restart_") {
		phase := "prepared"
		if scenario == "restart_upstream" {
			phase = "upstream"
		}
		assertApplicationCoordination(t, activator.directory, phase, false)
		pending, err := client.State(ctx)
		if err != nil || pending.Status != "staged" {
			t.Fatal("重建前必须保留真实代理未完成事务", pending, err)
		}
	} else {
		assertApplicationCoordination(t, activator.directory, want, true)
	}
	proxyLeaf := leaf
	if scenario == "restart_staged" {
		proxyLeaf = f.newLeaf
	}
	if _, err := f.proxy.Check(ctx, proxyLeaf); err != nil {
		t.Fatal("重建前代理证书与预期阶段不一致", err)
	}
	if _, err := f.upstream.Check(ctx, leaf); err != nil {
		t.Fatal("重建前应用监听证书与预期阶段不一致", err)
	}
	assertApplicationTLSConfig(t, f, want)
	stop()
	restarted, _ := f.start(t)
	recovered := coordinatedManagementActivator{server: restarted.HTTP, directory: activator.directory, proxy: client}
	// 使用实际应用恢复工作循环，首次完成后停止，避免与后续断言抢占独占会话。
	completed := make(chan error, 1)
	restarted.startManagementRecovery(func(ctx context.Context) error {
		err := recovered.coordinator(nil).Recover(ctx)
		completed <- err
		return err
	})
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal("应用恢复工作循环失败", err)
		}
	case <-ctx.Done():
		t.Fatal("应用恢复工作循环超时", ctx.Err())
	}
	restarted.managementStop()
	<-restarted.managementDone
	assertApplicationCoordination(t, recovered.directory, want, true)
	assertApplicationTLSConfig(t, f, want)
	for _, endpoint := range []nginxactivation.Endpoint{f.proxy, f.upstream} {
		if err := endpoint.Wait(ctx, leaf); err != nil {
			t.Fatal("重建应用后真实双层证书未收敛", err)
		}
	}
	actual, err := client.State(ctx)
	if err != nil || actual.Status != want {
		t.Fatal("代理最终事务状态错误", actual, err)
	}
	var snapshot nginxactivation.ManagedCertificate
	if err := (managementUpstream{server: restarted.HTTP}).WithCertificate(ctx, func(session nginxactivation.ManagedSession) error {
		var err error
		snapshot, err = session.Snapshot()
		return err
	}); err != nil || snapshot.LeafHash != fmt.Sprintf("%x", sha256.Sum256(leaf)) {
		t.Fatal("重建后应用管理快照错误", err)
	}
	current, err := restarted.HTTP.ManagementCertificate()
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Ready(ctx, current); err != nil {
		t.Fatal("恢复后未允许后续续期", err)
	}
	f.assertDatabase(t, restarted)
}

func assertApplicationTLSConfig(t *testing.T, f *applicationTLSFixture, phase string) {
	t.Helper()
	actual, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	want := f.config
	if phase == "committed" {
		want.Server.TLSCert, want.Server.TLSKey = f.candidate.FullChainPath, f.candidate.PrivateKeyPath
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("双层协调改变了证书路径以外的配置，或证书路径未按终态持久化")
	}
}

func assertApplicationCoordination(t *testing.T, directory, phase string, terminal bool) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, "coordination.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Phase     string `json:"phase"`
		RequestID string `json:"request_id"`
		ProxyID   string `json:"proxy_id"`
	}
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	if record.Phase != phase || record.RequestID == "" || (terminal && record.RequestID != record.ProxyID) {
		t.Fatalf("应用协调记录错误：%+v，预期阶段 %s", record, phase)
	}
}
