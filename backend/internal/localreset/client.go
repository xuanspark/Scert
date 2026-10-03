package localreset

import (
	"bufio"
	"context"
	"net"
	"sync"
	"time"
)

type Client struct {
	conn              net.Conn
	reader            *bufio.Reader
	nonce             string
	methods           []Method
	recoveryAvailable bool
	stage             int
	stop              func() bool
	mu                sync.Mutex
	once              sync.Once
}

// Begin 只接受已通过 Dial 校验身份的连接；调用过程不会通过参数或文件传递密码。
func Begin(ctx context.Context, conn net.Conn) (*Client, error) {
	if conn == nil {
		return nil, ErrUnavailable
	}
	deadline := time.Now().Add(SessionTimeout)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	client := &Client{conn: conn, reader: bufio.NewReaderSize(conn, MaxFrameSize+1)}
	client.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	var response Response
	if err := readFrame(client.reader, &response); err != nil || !validChallenge(response) ||
		len(response.Methods) == 0 || len(response.Methods) > 8 || response.RecoveryPage != nil || response.Inspection != nil || response.InspectionPart != nil {
		_ = client.Close()
		return nil, ErrProtocol
	}
	client.nonce = response.Nonce
	client.methods = response.Methods
	client.recoveryAvailable = response.RecoveryAvailable
	return client, nil
}

func (c *Client) Methods() []Method { return append([]Method(nil), c.methods...) }
func (c *Client) Select(method string) error {
	return c.exchange(Request{Action: "select", Method: method}, 0)
}
func (c *Client) Verify(code string) error {
	return c.exchange(Request{Action: "verify", Code: code}, 1)
}
func (c *Client) Reset(password string) error {
	return c.exchange(Request{Action: "reset", Password: password}, 2)
}

func (c *Client) exchange(request Request, stage int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != stage {
		_ = c.Close()
		return ErrProtocol
	}
	request.Version, request.Nonce = Version, c.nonce
	if !validRequest(request, c.nonce, request.Action) || writeFrame(c.conn, request) != nil {
		c.stage = -1
		_ = c.Close()
		return ErrProtocol
	}
	var response Response
	if err := readFrame(c.reader, &response); err != nil {
		c.stage = -1
		_ = c.Close()
		return ErrProtocol
	}
	if response.RecoveryAvailable || response.RecoveryPage != nil || response.Inspection != nil || response.InspectionPart != nil {
		c.stage = -1
		_ = c.Close()
		return ErrProtocol
	}
	if stage == 2 {
		c.stage = -1
		_ = c.Close()
		if response.Version != Version || !response.OK || response.Code != "COMPLETE" ||
			response.Nonce != "" || len(response.Methods) != 0 {
			return ErrProtocol
		}
		return nil
	}
	if !validChallenge(response) || response.Nonce == c.nonce || len(response.Methods) != 0 {
		c.stage = -1
		_ = c.Close()
		return ErrProtocol
	}
	c.nonce = response.Nonce
	c.stage++
	return nil
}

func validChallenge(response Response) bool {
	if response.Version != Version || !response.OK || response.Code != "CHALLENGE" || len(response.Nonce) != 64 {
		return false
	}
	for _, value := range response.Nonce {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return false
		}
	}
	return true
}

func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
		err = c.conn.Close()
	})
	return err
}
