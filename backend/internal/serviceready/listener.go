package serviceready

import (
	"net"
	"sync"
)

// Listener 在服务进入接收循环时通知一次；通知失败时不接收连接。
type Listener struct {
	net.Listener
	Notify func() error
	once   sync.Once
	err    error
}

func (l *Listener) Accept() (net.Conn, error) {
	l.once.Do(func() {
		if l.Notify != nil {
			l.err = l.Notify()
		}
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.Listener.Accept()
}
