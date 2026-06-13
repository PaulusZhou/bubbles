package ipc

import (
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"sync"
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
}

func (s *Server) Listen() error {
	// 清理旧 socket 文件
	os.Remove(s.socketPath)

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = l

	// 设置 socket 文件权限
	os.Chmod(s.socketPath, 0600)

	slog.Info("IPC server listening", "socket", s.socketPath)

	go s.acceptLoop()
	return nil
}

func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// listener 已关闭
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req Request
	if err := decoder.Decode(&req); err != nil {
		slog.Warn("invalid request", "error", err)
		encoder.Encode(Response{Error: "invalid request"})
		return
	}

	s.mu.RLock()
	handler, ok := s.handlers[req.Method]
	s.mu.RUnlock()

	if !ok {
		encoder.Encode(Response{Error: "unknown method: " + req.Method})
		return
	}

	// 将 params 序列化为 RawMessage 传给 handler
	rawParams, _ := json.Marshal(req.Params)

	result, err := handler(rawParams)
	if err != nil {
		encoder.Encode(Response{Error: err.Error()})
		return
	}

	encoder.Encode(Response{Result: result})
}
