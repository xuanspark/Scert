package deployment

import (
	"sync"
	"testing"

	"github.com/pkg/sftp"
)

func TestSSHCloseHealthyTransportDoesNotReportSFTPShutdownEOF(t *testing.T) {
	factory, server := safetyFactory(t, requestSFTPHandler(sftp.InMemHandler(), false, false))
	client, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.MkdirAll(t.Context(), "/close-check"); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("正常关闭连接不得把本机主动关闭产生的错误当作部署失败：%v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("重复关闭应返回相同的成功结果：%v", err)
	}
}

func TestSSHCloseConcurrentCallsShareResult(t *testing.T) {
	factory, server := safetyFactory(t, requestSFTPHandler(sftp.InMemHandler(), false, false))
	client, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var calls sync.WaitGroup
	for range 8 {
		calls.Go(func() {
			if err := client.Close(); err != nil {
				t.Errorf("并发关闭不得制造伪失败：%v", err)
			}
		})
	}
	calls.Wait()
}

func TestSSHClosePreservesPriorTransportFailure(t *testing.T) {
	factory, server := safetyFactory(t, requestSFTPHandler(sftp.InMemHandler(), false, false))
	client, err := factory.Connect(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	adapter := client.(*sshClientAdapter)
	adapter.disconnected.Store(true)
	if err := adapter.ssh.Close(); err != nil {
		t.Fatal(err)
	}
	first := client.Close()
	if first == nil {
		t.Fatal("既有传输失败不能被正常关闭分支吞掉")
	}
	if second := client.Close(); second != first {
		t.Fatalf("重复关闭必须保留首次失败：%v / %v", first, second)
	}
}
