package serviceready

import (
	"errors"
	"net"
	"testing"
)

type testListener struct {
	net.Listener
	accepts int
}

func (l *testListener) Accept() (net.Conn, error) {
	l.accepts++
	return nil, net.ErrClosed
}

func TestListenerNotifyOnce(t *testing.T) {
	for _, fail := range []bool{false, true} {
		base := &testListener{}
		calls := 0
		expected := errors.New("通知失败")
		listener := &Listener{Listener: base, Notify: func() error {
			calls++
			if base.accepts != 0 {
				t.Fatal("开始接收连接后才通知")
			}
			if fail {
				return expected
			}
			return nil
		}}
		for range 2 {
			_, err := listener.Accept()
			if fail && !errors.Is(err, expected) {
				t.Fatal("通知失败未终止接收")
			}
		}
		if calls != 1 || (fail && base.accepts != 0) || (!fail && base.accepts != 2) {
			t.Fatalf("通知或接收次数错误：%d/%d", calls, base.accepts)
		}
	}
}

func TestListenerWithoutNotifier(t *testing.T) {
	base := &testListener{}
	_, err := (&Listener{Listener: base}).Accept()
	if !errors.Is(err, net.ErrClosed) || base.accepts != 1 {
		t.Fatal("未配置通知时改变了接收行为")
	}
}
