package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/database"
	"git.pcux.cn/xuans/Scert/backend/internal/nginxactivation"
	"git.pcux.cn/xuans/Scert/backend/internal/secretstore"
)

type managementListenerFixture struct {
	material *rotationFixture
	path     string
	endpoint nginxactivation.Endpoint
}

func newManagementListenerFixture(t *testing.T) *managementListenerFixture {
	t.Helper()
	material := newRotationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := material.server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(material.server.configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(material.address)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Port, err = strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.HTTPSMode = "proxy"
	cfg.Server.ProxyHTTPSPort = 443
	cfg.DNS.Provider = "aliyun"
	cfg.DNS.CredentialRef = readinessDNSReference
	if err := cfg.Save(material.server.configPath); err != nil {
		t.Fatal(err)
	}
	store, err := secretstore.Open(filepath.Join(cfg.Paths.Data, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), readinessDNSReference, map[string]string{
		"access_key_id": "listener-test-id", "access_key_secret": "listener-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	return &managementListenerFixture{
		material: material, path: material.server.configPath,
		endpoint: nginxactivation.Endpoint{Address: material.address, Hostname: cfg.Web.Hostname, Roots: material.pool},
	}
}

func (f *managementListenerFixture) start(t *testing.T) (*Server, *database.Database, func()) {
	t.Helper()
	cfg, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := database.Migrate(t.Context(), db.DB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	store, err := secretstore.Open(filepath.Join(cfg.Paths.Data, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	server := New(Dependencies{Config: cfg, ConfigPath: f.path, Secrets: store, ReadinessCheck: db.Ping})
	server.tlsRoots = f.material.pool
	done := make(chan struct{})
	var serveErr error
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(ctx); err != nil {
				t.Error(err)
			}
			select {
			case <-done:
				if !errors.Is(serveErr, http.ErrServerClosed) {
					t.Errorf("正式监听入口退出异常：%v", serveErr)
				}
			case <-ctx.Done():
				t.Error("正式监听入口未按时退出")
			}
		})
	}
	t.Cleanup(stop)
	go func() {
		serveErr = server.ListenAndServe()
		close(done)
	}()
	select {
	case <-server.Listening():
	case <-done:
		t.Fatalf("正式监听入口启动失败：%v", serveErr)
	case <-time.After(5 * time.Second):
		t.Fatal("正式监听入口未按时开始接收连接")
	}
	return server, db, stop
}

func managementSnapshot(t *testing.T, server *Server) ManagementSnapshot {
	t.Helper()
	var snapshot ManagementSnapshot
	if err := server.WithManagementCertificate(t.Context(), func(session *ManagementCertificateSession) error {
		var err error
		snapshot, err = session.Snapshot()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *managementListenerFixture) candidate(t *testing.T) (certificate.SavedResource, []byte, string) {
	t.Helper()
	chain, key := f.material.issue(t, 3, f.endpoint.Hostname, time.Now().Add(-time.Hour), time.Now().Add(30*time.Hour))
	pair, err := tls.LoadX509KeyPair(chain, key)
	if err != nil {
		t.Fatal(err)
	}
	return certificate.SavedResource{FullChainPath: chain, PrivateKeyPath: key}, pair.Certificate[0], fmt.Sprintf("%x", sha256.Sum256(pair.Certificate[0]))
}

func TestManagementSessionPersistsAcrossRealListenerRestart(t *testing.T) {
	f := newManagementListenerFixture(t)
	server, db, stop := f.start(t)
	previous := managementSnapshot(t, server)
	if previous.UpstreamAddress != f.endpoint.Address || previous.ProxyAddress != "127.0.0.1:443" {
		t.Fatal("管理快照与实际监听地址不一致")
	}
	if _, err := f.endpoint.Check(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(t.Context(), "CREATE TABLE activation_test (value TEXT NOT NULL); INSERT INTO activation_test VALUES ('preserved')"); err != nil {
		t.Fatal(err)
	}
	before, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	saved, leaf, hash := f.candidate(t)
	if err := server.WithManagementCertificate(t.Context(), func(session *ManagementCertificateSession) error {
		if err := session.Activate(previous, saved, hash); err != nil {
			return err
		}
		// 独占协调期间仍须通过真实 TLS、凭据存储和数据库就绪检查。
		_, err := f.endpoint.Check(t.Context(), leaf)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	before.Server.TLSCert, before.Server.TLSKey = saved.FullChainPath, saved.PrivateKeyPath
	if !reflect.DeepEqual(stored, before) {
		t.Fatal("会话切换应只更新证书路径，保留其他持久化配置")
	}
	stop()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, reopened, _ := f.start(t)
	if snapshot := managementSnapshot(t, restarted); snapshot.LeafHash != hash || snapshot.State.CertificatePath != saved.FullChainPath {
		t.Fatal("重启后管理快照未恢复已持久化的候选")
	}
	if _, err := f.endpoint.Check(t.Context(), leaf); err != nil {
		t.Fatal("重启后真实 HTTPS 未提供候选证书或未就绪", err)
	}
	var value string
	if err := reopened.DB.QueryRowContext(t.Context(), "SELECT value FROM activation_test").Scan(&value); err != nil || value != "preserved" {
		t.Fatal("重启后数据库内容未保留", err)
	}
}

func TestManagementSessionRecoversPersistedCandidateAfterInterruptedSnapshot(t *testing.T) {
	f := newManagementListenerFixture(t)
	server, db, stop := f.start(t)
	oldLeaf, err := f.endpoint.Check(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, leaf, hash := f.candidate(t)
	cfg, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟原子配置替换完成、内存证书切换尚未执行时的进程中断窗口。
	cfg.Server.TLSCert, cfg.Server.TLSKey = saved.FullChainPath, saved.PrivateKeyPath
	if err := cfg.Save(f.path); err != nil {
		t.Fatal(err)
	}
	if err := server.WithManagementCertificate(t.Context(), func(session *ManagementCertificateSession) error {
		_, err := session.Snapshot()
		return err
	}); err == nil {
		t.Fatal("磁盘与运行证书不一致时必须拒绝协调")
	}
	if _, err := f.endpoint.Check(t.Context(), oldLeaf); err != nil {
		t.Fatal("不一致窗口应保留已有 TLS 服务", err)
	}
	stop()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, _, _ := f.start(t)
	if snapshot := managementSnapshot(t, restarted); snapshot.LeafHash != hash || snapshot.State.CertificatePath != saved.FullChainPath {
		t.Fatal("重启没有从配置恢复新的管理证书")
	}
	if _, err := f.endpoint.Check(t.Context(), leaf); err != nil {
		t.Fatal("中断恢复后真实 HTTPS 未通过候选证书检查", err)
	}
}

func TestManagementListenerReadinessRejectsClosedDatabase(t *testing.T) {
	f := newManagementListenerFixture(t)
	_, db, _ := f.start(t)
	if _, err := f.endpoint.Check(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.endpoint.Check(t.Context(), nil); err == nil {
		t.Fatal("真实 HTTPS 探测不能将数据库不可用视为就绪")
	}
}
