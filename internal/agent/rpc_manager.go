package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// defaultRPCIdleTTL is how long an idle RPC session survives before reaping.
const defaultRPCIdleTTL = 30 * time.Minute

// RPCManager pools long-lived pi RPC sessions keyed by a host-level session
// key (e.g. a Feishu chat session). It lazily starts pi processes, restarts
// crashed ones, and reaps idle sessions.
type RPCManager struct {
	piPath  string
	workDir string
	model   string
	idleTTL time.Duration

	mu       sync.Mutex
	sessions map[string]*managedRPCSession
}

type managedRPCSession struct {
	*RPCSession
	lastUsed time.Time
}

// NewRPCManager creates a manager that starts pi RPC sessions with the given
// CLI path and options. idleTTL <= 0 uses the default.
func NewRPCManager(piPath, workDir, model string, idleTTL time.Duration) *RPCManager {
	if idleTTL <= 0 {
		idleTTL = defaultRPCIdleTTL
	}
	return &RPCManager{
		piPath:   piPath,
		workDir:  workDir,
		model:    model,
		idleTTL:  idleTTL,
		sessions: map[string]*managedRPCSession{},
	}
}

// Get returns the session for key, starting a pi process if needed.
// resumeSessionID is honored only when the underlying session is (re)created;
// a live session is already bound to its pi session.
func (m *RPCManager) Get(ctx context.Context, key, resumeSessionID string) (*RPCSession, error) {
	m.mu.Lock()
	if ms, ok := m.sessions[key]; ok {
		select {
		case <-ms.Done():
			// The process died; drop it and recreate below.
			delete(m.sessions, key)
			slog.Info("pi rpc manager: replacing dead session", "key", key)
		default:
			ms.lastUsed = time.Now()
			s := ms.RPCSession
			m.mu.Unlock()
			return s, nil
		}
	}
	m.mu.Unlock()

	s := NewRPCSession(m.piPath, m.workDir, m.model, resumeSessionID)
	if err := s.Start(ctx); err != nil {
		return nil, err
	}

	ms := &managedRPCSession{RPCSession: s, lastUsed: time.Now()}
	m.mu.Lock()
	if existing, ok := m.sessions[key]; ok {
		// Lost a start race; keep the winner.
		m.mu.Unlock()
		_ = s.Close()
		return existing.RPCSession, nil
	}
	m.sessions[key] = ms
	m.mu.Unlock()
	slog.Info("pi rpc manager: session started", "key", key, "resume_session_id", resumeSessionID)
	return s, nil
}

// Peek returns the live session for key without starting one, or nil.
func (m *RPCManager) Peek(key string) *RPCSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.sessions[key]
	if !ok {
		return nil
	}
	select {
	case <-ms.Done():
		delete(m.sessions, key)
		return nil
	default:
	}
	ms.lastUsed = time.Now()
	return ms.RPCSession
}

// Close closes and removes the session for key, if any.
func (m *RPCManager) Close(key string) {
	m.mu.Lock()
	ms, ok := m.sessions[key]
	if ok {
		delete(m.sessions, key)
	}
	m.mu.Unlock()
	if ok {
		_ = ms.Close()
		slog.Info("pi rpc manager: session closed", "key", key)
	}
}

// CloseAll closes every managed session.
func (m *RPCManager) CloseAll() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*managedRPCSession{}
	m.mu.Unlock()
	for key, ms := range sessions {
		_ = ms.Close()
		slog.Info("pi rpc manager: session closed", "key", key)
	}
}

// Reap closes sessions that have been idle longer than the TTL. Sessions with
// an in-flight run are never reaped.
func (m *RPCManager) Reap(now time.Time) {
	m.mu.Lock()
	var expired []*managedRPCSession
	for key, ms := range m.sessions {
		if now.Sub(ms.lastUsed) > m.idleTTL && !ms.Running() {
			delete(m.sessions, key)
			expired = append(expired, ms)
			slog.Info("pi rpc manager: reaping idle session", "key", key)
		}
	}
	m.mu.Unlock()
	for _, ms := range expired {
		_ = ms.Close()
	}
}

// RunJanitor reaps idle sessions every `every` until ctx is canceled.
func (m *RPCManager) RunJanitor(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.CloseAll()
			return
		case now := <-ticker.C:
			m.Reap(now)
		}
	}
}
