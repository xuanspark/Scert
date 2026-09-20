package localreset

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type authorizationTestConnection struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *authorizationTestConnection) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type authorizationTestListener struct {
	pending chan net.Conn
	closed  chan struct{}
	once    sync.Once
}

func (l *authorizationTestListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.pending:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *authorizationTestListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
func (l *authorizationTestListener) Addr() net.Addr { return nil }

// 校验原生句柄期间不能关闭连接；否则底层句柄可能已释放或被其他对象复用。
func TestAuthorizationCannotRaceWithServerClose(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	conn := &authorizationTestConnection{Conn: left, closed: make(chan struct{})}
	defer conn.Close()
	listener := &authorizationTestListener{pending: make(chan net.Conn, 1), closed: make(chan struct{})}
	listener.pending <- conn
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var closedDuringAuthorization atomic.Bool
	server := newServer(listener, func(net.Conn) (Peer, error) {
		close(entered)
		<-release
		select {
		case <-conn.closed:
			closedDuringAuthorization.Store(true)
		default:
		}
		return Peer{}, ErrUnauthorized
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(ctx, func(context.Context, Peer) (Session, error) {
			t.Error("校验失败后仍调用了重置业务")
			return nil, ErrUnavailable
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("身份校验未启动")
	}
	closing := make(chan struct{})
	closed := make(chan error, 1)
	go func() { close(closing); closed <- server.Close() }()
	<-closing
	select {
	case <-conn.closed:
		t.Error("身份校验尚未完成，连接却已被关闭")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-closed:
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Fatal("释放校验后服务仍无法关闭")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Fatal("服务关闭没有等待连接处理完成")
	}
	if closedDuringAuthorization.Load() {
		t.Fatal("身份校验读取了已经关闭的原生连接")
	}
}
