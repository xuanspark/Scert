package processlock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquirePreventsConcurrentProcessAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scert.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("获取第一个进程锁失败：%v", err)
	}
	defer first.Close()

	if _, err := Acquire(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("并发进程锁未被拒绝：%v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("释放第一个进程锁失败：%v", err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("释放后无法重新获取进程锁：%v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("释放第二个进程锁失败：%v", err)
	}
}
