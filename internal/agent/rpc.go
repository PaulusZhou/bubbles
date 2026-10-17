package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrRunInFlight is returned by RunPrompt when a run is already active on the
// session; callers can fall back to Steer/FollowUp.
var ErrRunInFlight = errors.New("pi rpc: a run is already in flight")

// RPCSession manages a long-lived `pi --mode rpc` subprocess and maps the
// JSONL protocol onto bubbles StreamEvents. One session binds to one pi
// session (chat conversation); it supports mid-run steering and follow-ups.
type RPCSession struct {
	piPath          string
	workDir         string
	model           string
	resumeSessionID string

	writeMu sync.Mutex // serializes stdin writes

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	nextID     int64
	pending    map[string]chan rpcResponse
	mapper     *piEventMapper
	activeCb   EventCallback
	uiBridge   UIBridge
	activeDone chan struct{} // closed when the in-flight run settles
	lastRunErr error
	lastCbErr  error
	sessionID  string
	closed     bool
	stderr     stderrBuffer

	done chan struct{} // closed when the reader loop exits
}

// rpcResponse is a command response record from pi RPC mode.
type rpcResponse struct {
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// NewRPCSession creates a session that will run `pi --mode rpc`. If
// resumeSessionID is non-empty, pi starts with `--session <id>` to continue
// the previous conversation.
func NewRPCSession(piPath, workDir, model, resumeSessionID string) *RPCSession {
	if piPath == "" {
		piPath = "pi"
	}
	return &RPCSession{
		piPath:          piPath,
		workDir:         workDir,
		model:           model,
		resumeSessionID: resumeSessionID,
		pending:         map[string]chan rpcResponse{},
		done:            make(chan struct{}),
	}
}

// Start spawns the pi RPC subprocess and begins reading events. The
// subprocess is deliberately not bound to ctx: it is a long-lived pooled
// session that must outlive the request that started it and lives until
// Close (or the manager's janitor) reaps it. ctx only bounds the startup
// session-ID probe.
func (s *RPCSession) Start(ctx context.Context) error {
	args := piArgs("rpc", s.model, s.resumeSessionID)
	cmd := exec.Command(s.piPath, args...)
	if s.workDir != "" {
		cmd.Dir = s.workDir
	}
	cmd.Stderr = &s.stderr

	slog.Info("pi rpc: starting process", "path", s.piPath, "args", args, "dir", cmd.Dir)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pi rpc: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.mu.Unlock()

	slog.Info("pi rpc: process started", "pid", cmd.Process.Pid)

	go s.readLoop(stdout)
	go func() {
		<-s.done
		_ = cmd.Wait()
	}()

	// RPC mode emits no session header record (see pi docs/rpc.md), so probe
	// get_state to learn the session ID early. Best effort: RunPrompt probes
	// again before every run.
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, _ = s.refreshSessionID(probeCtx)
	return nil
}

// Done returns a channel closed when the pi process exits.
func (s *RPCSession) Done() <-chan struct{} { return s.done }

// SessionID returns the pi session ID reported by the session header, if seen.
func (s *RPCSession) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// Running reports whether a run is in flight.
func (s *RPCSession) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeCb != nil
}

// SetUIBridge sets the bridge that receives pi extension UI requests
// (confirmation dialogs, notifications). Pass nil to disable; dialog requests
// without a bridge are auto-cancelled.
func (s *RPCSession) SetUIBridge(bridge UIBridge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uiBridge = bridge
}

// RunPrompt executes one full agent run: it sends the prompt, streams mapped
// events to cb, and returns after the session settles (agent_settled). It
// fails if a run is already in flight.
func (s *RPCSession) RunPrompt(ctx context.Context, message string, cb EventCallback) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("pi rpc session closed")
	}
	if s.activeCb != nil {
		s.mu.Unlock()
		return ErrRunInFlight
	}
	s.mapper = newPiEventMapper()
	s.activeCb = cb
	done := make(chan struct{})
	s.activeDone = done
	s.mu.Unlock()

	// RPC mode emits no session header record, and the ID can change across
	// fork / new_session / switch_session. Probe get_state and replay the
	// current ID as a system event so the host can store it for resume.
	if _, err := s.refreshSessionID(ctx); err != nil {
		slog.Debug("pi rpc: session id probe failed", "error", err)
	}
	if sessionID := s.SessionID(); sessionID != "" {
		s.emit(cb, StreamEvent{Type: "system", SessionID: sessionID})
	}

	resp, err := s.command(ctx, map[string]interface{}{"type": "prompt", "message": message})
	if err != nil {
		s.finishRun()
		return fmt.Errorf("send prompt: %w", err)
	}
	if !resp.Success {
		s.finishRun()
		return fmt.Errorf("prompt rejected: %s", resp.Error)
	}
	var data struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(resp.Data, &data)
	if data.Disposition == "handled" {
		// An extension command consumed the prompt; no run started.
		s.finishRun()
		return nil
	}

	select {
	case <-done:
		s.mu.Lock()
		runErr := s.lastRunErr
		cbErr := s.lastCbErr
		s.lastCbErr = nil
		s.mu.Unlock()
		if cbErr != nil {
			return fmt.Errorf("stream callback error: %w", cbErr)
		}
		return runErr
	case <-s.done:
		s.finishRun()
		return fmt.Errorf("pi rpc process exited%s", s.stderr.suffix())
	case <-ctx.Done():
		// Best-effort abort so pi stops working on the canceled request.
		abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.Abort(abortCtx)
		cancel()
		return fmt.Errorf("pi rpc run canceled: %w", ctx.Err())
	}
}

// Steer queues a message delivered mid-run, after the current tool calls
// finish and before the next LLM call. Requires an in-flight run.
func (s *RPCSession) Steer(ctx context.Context, message string) error {
	return s.queueMessage(ctx, "steer", message)
}

// FollowUp queues a message delivered after the current run finishes.
func (s *RPCSession) FollowUp(ctx context.Context, message string) error {
	return s.queueMessage(ctx, "follow_up", message)
}

func (s *RPCSession) queueMessage(ctx context.Context, kind, message string) error {
	if !s.Running() {
		return errors.New("pi rpc: no run in flight to " + kind)
	}
	resp, err := s.command(ctx, map[string]interface{}{"type": kind, "message": message})
	if err != nil {
		return fmt.Errorf("send %s: %w", kind, err)
	}
	if !resp.Success {
		return fmt.Errorf("%s rejected: %s", kind, resp.Error)
	}
	return nil
}

// Abort aborts the in-flight run and waits for the session to become idle.
func (s *RPCSession) Abort(ctx context.Context) error {
	resp, err := s.command(ctx, map[string]interface{}{"type": "abort"})
	if err != nil {
		return fmt.Errorf("send abort: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("abort failed: %s", resp.Error)
	}
	return nil
}

// GetState returns the current session state record.
func (s *RPCSession) GetState(ctx context.Context) (json.RawMessage, error) {
	resp, err := s.command(ctx, map[string]interface{}{"type": "get_state"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("get_state failed: %s", resp.Error)
	}
	return resp.Data, nil
}

// refreshSessionID fetches the current session ID from pi via get_state and
// records it. RPC mode never emits the JSON-mode session header record, so
// get_state is the source of truth; the ID can also change across fork,
// new_session, and switch_session. It returns the recorded session ID.
func (s *RPCSession) refreshSessionID(ctx context.Context) (string, error) {
	data, err := s.GetState(ctx)
	if err != nil {
		return s.SessionID(), err
	}
	var st struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return s.SessionID(), fmt.Errorf("decode get_state: %w", err)
	}
	if st.SessionID != "" {
		s.mu.Lock()
		s.sessionID = st.SessionID
		s.mu.Unlock()
	}
	return s.SessionID(), nil
}

// SetModel switches the model of the running pi process.
func (s *RPCSession) SetModel(ctx context.Context, model string) error {
	resp, err := s.command(ctx, map[string]interface{}{"type": "set_model", "model": model})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("set_model failed: %s", resp.Error)
	}
	s.mu.Lock()
	s.model = model
	s.mu.Unlock()
	return nil
}

// NewSession starts a fresh pi session within the running process.
func (s *RPCSession) NewSession(ctx context.Context) error {
	resp, err := s.command(ctx, map[string]interface{}{"type": "new_session"})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("new_session failed: %s", resp.Error)
	}
	return nil
}

// SwitchSession switches to an existing pi session file.
func (s *RPCSession) SwitchSession(ctx context.Context, sessionPath string) error {
	resp, err := s.command(ctx, map[string]interface{}{"type": "switch_session", "sessionPath": sessionPath})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("switch_session failed: %s", resp.Error)
	}
	return nil
}

// ForkMessage is a user message available for forking.
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// GetForkMessages lists the user messages that can be forked from on the
// active branch, oldest first.
func (s *RPCSession) GetForkMessages(ctx context.Context) ([]ForkMessage, error) {
	resp, err := s.command(ctx, map[string]interface{}{"type": "get_fork_messages"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("get_fork_messages failed: %s", resp.Error)
	}
	var data struct {
		Messages []ForkMessage `json:"messages"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, fmt.Errorf("decode get_fork_messages: %w", err)
	}
	return data.Messages, nil
}

// Fork moves the session to a new branch at the given user message and
// returns the original prompt text so the caller can re-run it. It reports
// cancelled when an extension vetoed the fork.
func (s *RPCSession) Fork(ctx context.Context, entryID string) (string, bool, error) {
	resp, err := s.command(ctx, map[string]interface{}{"type": "fork", "entryId": entryID})
	if err != nil {
		return "", false, err
	}
	if !resp.Success {
		return "", false, fmt.Errorf("fork failed: %s", resp.Error)
	}
	var data struct {
		Text      string `json:"text"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", false, fmt.Errorf("decode fork: %w", err)
	}
	return data.Text, data.Cancelled, nil
}

// GetLastAssistantText returns the final text of the last assistant message.
func (s *RPCSession) GetLastAssistantText(ctx context.Context) (string, error) {
	resp, err := s.command(ctx, map[string]interface{}{"type": "get_last_assistant_text"})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("get_last_assistant_text failed: %s", resp.Error)
	}
	var data struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("decode get_last_assistant_text: %w", err)
	}
	return data.Text, nil
}

// GetSessionStats returns the session statistics record (tokens, cost, ...).
func (s *RPCSession) GetSessionStats(ctx context.Context) (json.RawMessage, error) {
	resp, err := s.command(ctx, map[string]interface{}{"type": "get_session_stats"})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("get_session_stats failed: %s", resp.Error)
	}
	return resp.Data, nil
}

// ExportHTML writes an HTML rendering of the session to path. It returns the
// path pi actually wrote to (pi falls back to a default location when path is
// empty).
func (s *RPCSession) ExportHTML(ctx context.Context, path string) (string, error) {
	payload := map[string]interface{}{"type": "export_html"}
	if path != "" {
		payload["outputPath"] = path
	}
	resp, err := s.command(ctx, payload)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("export_html failed: %s", resp.Error)
	}
	var data struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("decode export_html: %w", err)
	}
	return data.Path, nil
}

// Close shuts down the pi process gracefully (stdin close), falling back to
// killing it after a short grace period.
func (s *RPCSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	stdin := s.stdin
	cmd := s.cmd
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-s.done
	}
	return nil
}

// command sends one RPC command and waits for its correlated response.
func (s *RPCSession) command(ctx context.Context, payload map[string]interface{}) (rpcResponse, error) {
	s.mu.Lock()
	if s.closed || s.stdin == nil {
		s.mu.Unlock()
		return rpcResponse{}, errors.New("pi rpc session not started")
	}
	s.nextID++
	id := strconv.FormatInt(s.nextID, 10)
	payload["id"] = id
	ch := make(chan rpcResponse, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	line, err := json.Marshal(payload)
	if err != nil {
		s.removePending(id)
		return rpcResponse{}, err
	}

	if err := s.writeRecordRaw(line); err != nil {
		s.removePending(id)
		return rpcResponse{}, err
	}
	slog.Debug("pi rpc: command sent", "command", string(line))

	select {
	case resp, ok := <-ch:
		if !ok {
			return rpcResponse{}, fmt.Errorf("pi rpc process exited%s", s.stderr.suffix())
		}
		return resp, nil
	case <-s.done:
		s.removePending(id)
		return rpcResponse{}, fmt.Errorf("pi rpc process exited%s", s.stderr.suffix())
	case <-ctx.Done():
		s.removePending(id)
		return rpcResponse{}, ctx.Err()
	}
}

func (s *RPCSession) removePending(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// writeRecord marshals and writes one JSONL record to pi's stdin.
func (s *RPCSession) writeRecord(v interface{}) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.writeRecordRaw(line)
}

func (s *RPCSession) writeRecordRaw(line []byte) error {
	s.mu.Lock()
	if s.closed || s.stdin == nil {
		s.mu.Unlock()
		return errors.New("pi rpc session not started")
	}
	stdin := s.stdin
	s.mu.Unlock()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write to pi rpc: %w", err)
	}
	return nil
}

// finishRun clears the active run state and closes its completion channel.
func (s *RPCSession) finishRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCb = nil
	if s.activeDone != nil {
		close(s.activeDone)
		s.activeDone = nil
	}
}

// readLoop consumes stdout records: responses are routed to pending commands,
// everything else is treated as a session event and mapped to StreamEvents.
func (s *RPCSession) readLoop(stdout io.Reader) {
	defer close(s.done)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		slog.Debug("pi rpc: record", "record", line)

		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			slog.Warn("pi rpc: failed to decode record", "error", err, "raw", line)
			continue
		}

		if probe.Type == "response" {
			var resp rpcResponse
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				slog.Warn("pi rpc: failed to decode response", "error", err, "raw", line)
				continue
			}
			s.mu.Lock()
			ch := s.pending[probe.ID]
			delete(s.pending, probe.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- resp
			}
			continue
		}

		if probe.Type == "extension_ui_request" {
			s.handleUIRequest([]byte(line))
			continue
		}

		s.dispatchEvent([]byte(line))
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		slog.Error("pi rpc: read loop failed", "error", err)
	}
	slog.Info("pi rpc: read loop exited")

	// Fail all in-flight waiters and the active run on process exit.
	s.mu.Lock()
	for id, ch := range s.pending {
		delete(s.pending, id)
		close(ch)
	}
	s.mu.Unlock()
}

// dispatchEvent maps one session event record and forwards the resulting
// StreamEvents to the active run's callback. On agent_settled the run is
// finished and its terminal "result" event delivered.
func (s *RPCSession) dispatchEvent(line []byte) {
	// Capture the session header even before any run starts.
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(line, &probe); err == nil && probe.Type == "session" && probe.ID != "" {
		s.mu.Lock()
		s.sessionID = probe.ID
		s.mu.Unlock()
	}

	s.mu.Lock()
	mapper := s.mapper
	cb := s.activeCb
	s.mu.Unlock()

	if mapper == nil {
		// Events outside a run (queue updates, extension noise): nothing to map.
		return
	}

	for _, ev := range mapper.Handle(line) {
		s.emit(cb, ev)
	}

	if mapper.Settled {
		if ev, ok := mapper.Finish(); ok {
			s.emit(cb, ev)
		}
		s.mu.Lock()
		s.lastRunErr = mapper.RunErr()
		s.mu.Unlock()
		s.finishRun()
	}
}

// handleUIRequest forwards an extension UI request to the bridge. Dialog
// requests are answered asynchronously so the read loop never blocks; without
// a bridge they are auto-cancelled so the extension resolves immediately.
func (s *RPCSession) handleUIRequest(line []byte) {
	var req UIRequest
	if err := json.Unmarshal(line, &req); err != nil {
		slog.Warn("pi rpc: failed to decode extension_ui_request", "error", err, "raw", string(line))
		return
	}
	slog.Info("pi rpc: extension ui request", "method", req.Method, "id", req.ID, "title", req.Title)

	s.mu.Lock()
	bridge := s.uiBridge
	s.mu.Unlock()

	if !req.IsDialog() {
		if bridge != nil {
			go bridge.HandleUIRequest(context.Background(), req)
		}
		return
	}

	go func() {
		var resp *UIResponse
		if bridge != nil {
			resp = bridge.HandleUIRequest(context.Background(), req)
		}
		if resp == nil {
			resp = NewUICancelResponse(req.ID)
		}
		if err := s.writeRecord(resp); err != nil {
			slog.Error("pi rpc: failed to send extension_ui_response", "id", req.ID, "error", err)
		}
	}()
}

// emit invokes the run callback, recording callback errors for RunPrompt.
func (s *RPCSession) emit(cb EventCallback, ev StreamEvent) {
	if cb == nil {
		return
	}
	if err := cb(ev); err != nil {
		slog.Error("pi rpc: event callback error", "type", ev.Type, "error", err)
		s.mu.Lock()
		if s.lastCbErr == nil {
			s.lastCbErr = err
		}
		s.mu.Unlock()
	}
}
