package deployment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type observedSFTP struct {
	handlers    sftp.Handlers
	mode        atomic.Uint32
	writes      atomic.Int32
	unsafeWrite atomic.Bool
	exclusive   atomic.Bool
	denyMode    bool
}

func (h *observedSFTP) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	h.exclusive.Store(request.Pflags().Excl && !request.Pflags().Trunc)
	writer, err := h.handlers.FilePut.Filewrite(request)
	if err != nil {
		return nil, err
	}
	return &observedWriter{WriterAt: writer, owner: h}, nil
}

func (h *observedSFTP) Filecmd(request *sftp.Request) error {
	if request.Method == "Setstat" {
		if h.denyMode {
			return errors.New("测试拒绝权限设置")
		}
		h.mode.Store(request.Attributes().Mode & 0o777)
	}
	return h.handlers.FileCmd.Filecmd(request)
}

type observedWriter struct {
	io.WriterAt
	owner *observedSFTP
}

func (w *observedWriter) WriteAt(data []byte, offset int64) (int, error) {
	w.owner.writes.Add(1)
	if w.owner.mode.Load() != 0o600 {
		w.owner.unsafeWrite.Store(true)
	}
	return w.WriterAt.WriteAt(data, offset)
}

func (w *observedWriter) Close() error {
	if closer, ok := w.WriterAt.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func safetyFactory(t *testing.T, handler func(ssh.Channel, <-chan *ssh.Request)) (SSHFactory, Server) {
	t.Helper()
	hostSigner, _ := newTestSSHSigner(t)
	server := startTestPasswordSSHServerWithHandler(t, func(password string) error {
		if password != "test-password" {
			return errors.New("unexpected test password")
		}
		return nil
	}, handler, hostSigner)
	model := testSSHServerModel(server, "secret://ssh/safety", ssh.FingerprintSHA256(hostSigner.PublicKey()))
	return SSHFactory{
		Secrets: staticSecretReader{model.PasswordRef: {"password": "test-password"}},
		Timeout: time.Second, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
	}, model
}
func requestSFTPHandler(handlers sftp.Handlers, stallVersion, stallCommand bool) func(ssh.Channel, <-chan *ssh.Request) {
	return func(channel ssh.Channel, requests <-chan *ssh.Request) {
		defer channel.Close()
		for request := range requests {
			switch request.Type {
			case "subsystem":
				_ = request.Reply(true, nil)
				if stallVersion {
					_, _ = io.Copy(io.Discard, channel)
					return
				}
				server := sftp.NewRequestServer(channel, handlers)
				_ = server.Serve()
				_ = server.Close()
				return
			case "exec":
				_ = request.Reply(true, nil)
				if stallCommand {
					for request := range requests {
						_ = request.Reply(false, nil)
					}
					return
				}
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			default:
				_ = request.Reply(false, nil)
			}
		}
	}
}

func TestSSHUploadRestrictsPermissionsBeforeWritingAndRejectsExistingFile(t *testing.T) {
	for _, denyMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "chmod_denied"}[denyMode], func(t *testing.T) {
			observer := &observedSFTP{handlers: sftp.InMemHandler(), denyMode: denyMode}
			handlers := observer.handlers
			handlers.FilePut = observer
			handlers.FileCmd = observer
			factory, server := safetyFactory(t, requestSFTPHandler(handlers, false, false))
			client, err := factory.Connect(t.Context(), server)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = client.Upload(t.Context(), "/key.tmp", []byte("private-test-key"), 0o600)
			if denyMode {
				if err == nil || observer.writes.Load() != 0 {
					t.Fatal("权限设置失败后不得写入私钥")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if observer.unsafeWrite.Load() || observer.writes.Load() == 0 || !observer.exclusive.Load() {
				t.Fatal("上传必须独占创建并在权限收紧后才写入")
			}
			if err := client.Upload(t.Context(), "/key.tmp", []byte("overwrite"), 0o600); err == nil {
				t.Fatal("不得覆盖已有上传目标")
			}
		})
	}
}

func TestSSHFactoryCancelsUnresponsiveSFTPInitialization(t *testing.T) {
	factory, server := safetyFactory(t, requestSFTPHandler(sftp.InMemHandler(), true, false))
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := factory.Connect(ctx, server); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SFTP 握手无响应必须超时：%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("SFTP 握手取消不及时")
	}
}

func TestSSHDeploymentLockIsSharedAcrossConnections(t *testing.T) {
	factory, server := safetyFactory(t, requestSFTPHandler(sftp.InMemHandler(), false, false))
	first, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	const path = "/certificate.scert-lock"
	if err := first.Lock(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(t.Context(), path); err == nil {
		t.Fatal("其他 SSH 连接不得取得同一个远程部署锁")
	}
	if err := first.Unlock(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(t.Context(), path); err != nil {
		t.Fatalf("释放锁后应允许后续部署：%v", err)
	}
	if err := second.Unlock(t.Context(), path); err != nil {
		t.Fatal(err)
	}
}

type stalledLister struct{ release <-chan struct{} }

func (l stalledLister) Filelist(*sftp.Request) (sftp.ListerAt, error) {
	<-l.release
	return nil, errors.New("测试结束")
}

func TestSSHOperationsCancelUnresponsiveServer(t *testing.T) {
	for _, operation := range []string{"stat", "command"} {
		t.Run(operation, func(t *testing.T) {
			handlers := sftp.InMemHandler()
			release := make(chan struct{})
			defer close(release)
			if operation == "stat" {
				handlers.FileList = stalledLister{release: release}
			}
			factory, server := safetyFactory(t, requestSFTPHandler(handlers, false, true))
			client, err := factory.Connect(t.Context(), server)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			if operation == "stat" {
				_, err = client.Exists(ctx, "/test")
			} else {
				_, err = client.Run(ctx, []string{"sleep", "60"})
			}
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
				t.Fatalf("无响应操作未及时取消：%v", err)
			}
			if !client.(*sshClientAdapter).Disconnected() {
				t.Fatal("取消后必须标记传输已断开以触发重连回滚")
			}
		})
	}
}

func TestSSHDeploymentReconnectsAndRollsBackAfterCommandTimeout(t *testing.T) {
	handlers := sftp.InMemHandler()
	factory, server := safetyFactory(t, requestSFTPHandler(handlers, false, true))
	client, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MkdirAll(t.Context(), "/tmp"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
		if err := client.Upload(t.Context(), path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()
	report := NewService(factory).Deploy(ctx, []Target{{Server: server}}, testArtifact(t))
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("测试必须实际触发命令超时，而不是直接执行失败")
	}
	if report.Status != "failed" || !strings.Contains(report.Results[0].Message, "文件已回滚") || strings.Contains(report.Results[0].Message, "自动回滚失败") {
		t.Fatalf("超时后应重连完成文件回滚：%+v", report)
	}
	client, err = factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	adapter := client.(*sshClientAdapter)
	for _, path := range []string{server.CertificatePath, server.CertificateChainPath, server.PrivateKeyPath} {
		file, err := adapter.sftp.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil || !bytes.Equal(data, []byte("original")) {
			t.Fatalf("原文件未恢复：%s %v", path, err)
		}
	}
	files, err := adapter.sftp.ReadDir("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("回滚后残留事务文件：%v", files)
	}
}

func TestCommandOutputIsBounded(t *testing.T) {
	buffer := &commandOutput{}
	data := bytes.Repeat([]byte("x"), commandOutputLimit*3)
	if written, err := buffer.Write(data); written != len(data) || err != nil {
		t.Fatal("截断不应阻塞远程输出")
	}
	if len(buffer.Bytes()) != commandOutputLimit {
		t.Fatal("远程输出未限制内存占用")
	}
}
