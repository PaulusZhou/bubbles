package ipc

import (
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"
)

// Server listens on a Unix Socket and dispatches JSON-RPC requests to handlers.
type Server struct {
	socketPath string
	listener   net.Listener
	handlers   map[string]HandlerFunc
	mu         sync.RWMutex
}

type HandlerFunc func(params json.RawMessage) (interface{}, error)

func NewServer(socketPath string) *Server {
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]HandlerFunc),
	}
}

func (s *Server) Handle(method string, fn HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
	slog.Debug("ipc: handler registered", "method", method)
}

func (s *Server) Listen() error {
	// 清理旧 socket 文件
	os.Remove(s.socketPath)

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		slog.Error("ipc: failed to listen", "socket", s.socketPath, "error", err)
		return err
	}
	s.listener = l

	// 设置 socket 文件权限
	os.Chmod(s.socketPath, 0600)

	slog.Info("ipc server listening", "socket", s.socketPath)

	go s.acceptLoop()
	return nil
}

func (s *Server) Close() error {
	if s.listener != nil {
		slog.Info("ipc: server closing", "socket", s.socketPath)
		return s.listener.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// listener 已关闭
			slog.Info("ipc: accept loop stopped")
			return
		}
		remoteAddr := conn.RemoteAddr().Network()
		slog.Info("ipc: new connection accepted", "remote", remoteAddr)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	start := time.Now()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req Request
	if err := decoder.Decode(&req); err != nil {
		slog.Warn("ipc: invalid request received", "error", err)
		encoder.Encode(Response{Error: "invalid request"})
		return
	}

	slog.Info("ipc: request received", "method", req.Method)

	s.mu.RLock()
	handler, ok := s.handlers[req.Method]
	s.mu.RUnlock()

	if !ok {
		slog.Warn("ipc: unknown method", "method", req.Method)
		encoder.Encode(Response{Error: "unknown method: " + req.Method})
		return
	}

	// 将 params 序列化为 RawMessage 传给 handler
	rawParams, _ := json.Marshal(req.Params)

	result, err := handler(rawParams)
	duration := time.Since(start)

	if err != nil {
		slog.Error("ipc: handler returned error",
			"method", req.Method,
			"duration", duration,
			"error", err,
		)
		encoder.Encode(Response{Error: err.Error()})
		return
	}

	slog.Info("ipc: handler completed",
		"method", req.Method,
		"duration", duration,
	)
	encoder.Encode(Response{Result: result})
}
