package nginxactivation

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const operationTimeout = 60 * time.Second

// LocalService 只接受内核认证的指定 UID，不依赖请求提供的身份。
// 监听器由受信启动器提供；本服务不创建 TCP 入口或删除 socket 文件。
type LocalService struct {
	Rotation   Rotation
	AllowedUID uint32
}

func peerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if socketErr != nil {
		return 0, socketErr
	}
	if credentials == nil || credentials.Pid <= 0 {
		return 0, errors.New("本地激活连接缺少内核身份")
	}
	return credentials.Uid, nil
}

func (s LocalService) Serve(ctx context.Context, listener *net.UnixListener) error {
	if listener == nil {
		return errors.New("本地激活服务缺少 Unix 监听器")
	}
	if s.Rotation.Reloader == nil {
		return errors.New("本地激活策略未初始化")
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	var workers sync.WaitGroup
	defer workers.Wait()
	capacity := make(chan struct{}, 4)
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			cancel()
			if errors.Is(err, net.ErrClosed) && ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case capacity <- struct{}{}:
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-capacity }()
				s.handle(ctx, connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (s LocalService) handle(ctx context.Context, connection *net.UnixConn) {
	defer connection.Close()
	uid, err := peerUID(connection)
	if err != nil || uid != s.AllowedUID {
		return
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	var message request
	if err := readFrame(connection, &message); err != nil || !validRequest(message) {
		_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
		_ = writeFrame(connection, response{Version: protocolVersion, Code: "invalid_request"})
		return
	}
	operationCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	result := s.execute(operationCtx, message)
	// 私钥不写入日志、错误或响应，处理结束后清理请求缓冲。
	clear(message.Key)
	_ = connection.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_ = writeFrame(connection, result)
}

func validRequest(message request) bool {
	if message.Version != protocolVersion {
		return false
	}
	switch message.Action {
	case "ready", "state":
		return message.ID == "" && len(message.Chain) == 0 && len(message.Key) == 0
	case "stage":
		return message.ID == "" && len(message.Chain) > 0 && len(message.Chain) <= 1<<20 && len(message.Key) > 0 && len(message.Key) <= 64<<10
	case "commit", "reconcile":
		return validID(message.ID) && len(message.Chain) == 0 && len(message.Key) == 0
	default:
		return false
	}
}

func (s LocalService) execute(ctx context.Context, message request) response {
	result := response{Version: protocolVersion, Code: "ok"}
	var err error
	switch message.Action {
	case "ready":
		value, readyErr := s.Rotation.Ready(ctx)
		err = readyErr
		if err == nil {
			result.Ready = &value
		}
	case "state":
		value, stateErr := s.Rotation.State(ctx)
		err = stateErr
		if err == nil {
			result.State = &value
		}
	case "stage":
		result.ID, err = s.Rotation.Stage(ctx, message.Chain, message.Key)
	case "commit":
		err = s.Rotation.Commit(ctx, message.ID)
	case "reconcile":
		value, reconcileErr := s.Rotation.Reconcile(ctx, message.ID)
		err = reconcileErr
		if value.ID != "" {
			result.State = &value
		}
	}
	if err != nil {
		result.Code = "operation_failed"
	}
	return result
}
