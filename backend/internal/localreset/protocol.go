package localreset

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

const (
	Version        = 1
	MaxFrameSize   = 16 << 10
	SessionTimeout = 2 * time.Minute
	MaxConnections = 8
)

var (
	ErrUnauthorized = errors.New("本机调用者身份不受信任")
	ErrProtocol     = errors.New("本机重置请求无效或已失效")
	ErrUnavailable  = errors.New("本机重置通道不可用")
)

type Peer struct {
	Identity string
}

type Method struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Request struct {
	Version       int    `json:"version"`
	Nonce         string `json:"nonce"`
	Action        string `json:"action"`
	Method        string `json:"method,omitempty"`
	Code          string `json:"code,omitempty"`
	Password      string `json:"password,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	TransactionID string `json:"transaction_id,omitempty"`
}

type Response struct {
	Version           int                             `json:"version"`
	OK                bool                            `json:"ok"`
	Code              string                          `json:"code"`
	Nonce             string                          `json:"nonce,omitempty"`
	Methods           []Method                        `json:"methods,omitempty"`
	RecoveryAvailable bool                            `json:"recovery_available,omitempty"`
	RecoveryPage      *deployment.PendingRecoveryPage `json:"recovery_page,omitempty"`
	Inspection        *deployment.RecoveryInspection  `json:"inspection,omitempty"`
	InspectionPart    *inspectionPart                 `json:"inspection_part,omitempty"`
}

// Session 由主服务创建，身份验证及密码持久化仍由主服务负责。
type Session interface {
	Methods() []Method
	Select(context.Context, string) error
	Verify(context.Context, string) error
	Reset(context.Context, string) error
	Close()
}

type Factory func(context.Context, Peer) (Session, error)

type failureWindow struct {
	since time.Time
	count int
}

type Server struct {
	listener  net.Listener
	authorize func(net.Conn) (Peer, error)
	cleanup   func() error
	mu        sync.Mutex
	active    map[net.Conn]struct{}
	failures  map[string]failureWindow
	closed    bool
	serving   bool
	timeout   time.Duration
	now       func() time.Time
	once      sync.Once
	closeErr  error
	workers   sync.WaitGroup
}

func newServer(listener net.Listener, authorize func(net.Conn) (Peer, error), cleanup func() error) *Server {
	return &Server{listener: listener, authorize: authorize, cleanup: cleanup,
		active: make(map[net.Conn]struct{}), failures: make(map[string]failureWindow),
		timeout: SessionTimeout, now: time.Now}
}

// Serve 限制并发连接，取消时关闭全部连接并等待业务处理退出。
func (s *Server) Serve(ctx context.Context, factory Factory) error {
	if s == nil || factory == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	if s.closed || s.serving {
		s.mu.Unlock()
		return ErrUnavailable
	}
	s.serving = true
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	defer s.workers.Wait()
	defer s.Close()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.mu.Lock()
		if s.closed || len(s.active) >= MaxConnections {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.active[conn] = struct{}{}
		s.workers.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.workers.Done()
			defer conn.Close()
			defer func() { s.mu.Lock(); delete(s.active, conn); s.mu.Unlock() }()
			s.handle(ctx, conn, factory)
		}()
	}
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.closeErr = s.listener.Close()
		for conn := range s.active {
			_ = conn.Close()
		}
		s.mu.Unlock()
		if s.cleanup != nil {
			s.closeErr = errors.Join(s.closeErr, s.cleanup())
		}
	})
	return s.closeErr
}

func (s *Server) permit(identity string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, entry := range s.failures {
		if !now.Before(entry.since.Add(time.Minute)) {
			delete(s.failures, key)
		}
	}
	entry, found := s.failures[identity]
	return (!found && len(s.failures) < 128) || (found && entry.count < 5)
}

func (s *Server) failed(identity string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.failures[identity]
	if entry.since.IsZero() {
		entry.since = s.now()
	}
	entry.count++
	s.failures[identity] = entry
}

func (s *Server) handle(parent context.Context, conn net.Conn, factory Factory) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	peer, err := s.authorize(conn)
	s.mu.Unlock()
	if err != nil {
		_ = writeFrame(conn, Response{Version: Version, Code: "UNAUTHORIZED"})
		return
	}
	if !s.permit(peer.Identity) {
		_ = writeFrame(conn, Response{Version: Version, Code: "RATE_LIMITED"})
		return
	}
	session, err := factory(ctx, peer)
	if err != nil || session == nil {
		if session != nil {
			session.Close()
		}
		_ = writeFrame(conn, Response{Version: Version, Code: "UNAVAILABLE"})
		return
	}
	defer session.Close()
	methods := session.Methods()
	if len(methods) == 0 || len(methods) > 8 {
		_ = writeFrame(conn, Response{Version: Version, Code: "UNAVAILABLE"})
		return
	}
	reader := bufio.NewReaderSize(conn, MaxFrameSize+1)
	recovery, hasRecovery := session.(RecoverySession)
	hasRecovery = hasRecovery && recovery.RecoveryAvailable()
	for index, action := range []string{"select", "verify", "reset"} {
		nonce, err := newNonce()
		if err != nil {
			return
		}
		response := Response{Version: Version, OK: true, Code: "CHALLENGE", Nonce: nonce}
		if index == 0 {
			response.Methods = methods
			response.RecoveryAvailable = hasRecovery
		}
		if writeFrame(conn, response) != nil {
			return
		}
		var request Request
		err = readFrame(reader, &request)
		if err == nil && index == 0 && hasRecovery && validRecoveryRequest(request, nonce) {
			s.handleRecovery(ctx, conn, reader, peer, recovery, request)
			return
		}
		if err != nil || ctx.Err() != nil || !validRequest(request, nonce, action) {
			s.failed(peer.Identity)
			_ = writeFrame(conn, Response{Version: Version, Code: "INVALID_REQUEST"})
			return
		}
		switch action {
		case "select":
			allowed := false
			for _, method := range methods {
				if method.ID == request.Method {
					allowed = true
				}
			}
			if !allowed {
				err = ErrProtocol
			} else {
				err = session.Select(ctx, request.Method)
			}
		case "verify":
			err = session.Verify(ctx, request.Code)
		case "reset":
			err = session.Reset(ctx, request.Password)
		}
		if err != nil {
			s.failed(peer.Identity)
			_ = writeFrame(conn, Response{Version: Version, Code: "REJECTED"})
			return
		}
	}
	_ = writeFrame(conn, Response{Version: Version, OK: true, Code: "COMPLETE"})
}

func validRequest(request Request, nonce, action string) bool {
	if request.Cursor != "" || request.TransactionID != "" {
		return false
	}
	if request.Version != Version || request.Action != action ||
		subtle.ConstantTimeCompare([]byte(request.Nonce), []byte(nonce)) != 1 {
		return false
	}
	switch action {
	case "select":
		return request.Method != "" && len(request.Method) <= 128 && request.Code == "" && request.Password == ""
	case "verify":
		return request.Code != "" && len(request.Code) <= 128 && request.Method == "" && request.Password == ""
	case "reset":
		return request.Password != "" && len(request.Password) <= 256 && request.Method == "" && request.Code == ""
	}
	return false
}

func newNonce() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func readFrame(reader *bufio.Reader, value any) error {
	data, err := reader.ReadSlice('\n')
	if err != nil || len(data) > MaxFrameSize {
		return ErrProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrProtocol
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return ErrProtocol
	}
	return nil
}

func writeFrame(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil || len(data)+1 > MaxFrameSize {
		return ErrProtocol
	}
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
