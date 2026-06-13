package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	socketTimeout = 30 * time.Second
)

// Client sends JSON-RPC requests to the daemon over Unix Socket.
type Client struct {
	socketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

func (c *Client) Call(method string, params interface{}) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, socketTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w (is bubblesd running?)", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(socketTimeout))

	req := Request{Method: method, Params: params}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var resp Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return &resp, nil
}
