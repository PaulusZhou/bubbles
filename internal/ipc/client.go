package ipc

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	slog.Debug("ipc client: connecting", "socket", c.socketPath, "method", method)

	conn, err := net.DialTimeout("unix", c.socketPath, socketTimeout)
	if err != nil {
		slog.Error("ipc client: failed to connect to daemon",
			"socket", c.socketPath,
			"method", method,
			"error", err,
		)
		return nil, fmt.Errorf("connect to daemon: %w (is bubblesd running?)", err)
	}
	defer conn.Close()
	slog.Debug("ipc client: connected", "socket", c.socketPath)

	conn.SetDeadline(time.Now().Add(socketTimeout))

	req := Request{Method: method, Params: params}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		slog.Error("ipc client: failed to send request",
			"method", method,
			"error", err,
		)
		return nil, fmt.Errorf("send request: %w", err)
	}
	slog.Debug("ipc client: request sent", "method", method)

	var resp Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		slog.Error("ipc client: failed to read response",
			"method", method,
			"error", err,
		)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.Error != "" {
		slog.Warn("ipc client: daemon returned error",
			"method", method,
			"error", resp.Error,
		)
	} else {
		slog.Debug("ipc client: response received", "method", method)
	}

	return &resp, nil
}
