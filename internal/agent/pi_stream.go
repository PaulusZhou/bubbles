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
	"sort"
	"strings"
	"sync"
)

// stderrBuffer collects a child process's stderr for surfacing in errors.
type stderrBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *stderrBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// suffix returns a formatted stderr tail for error messages, or "" when empty.
func (s *stderrBuffer) suffix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := strings.TrimSpace(s.buf.String())
	if msg == "" {
		return ""
	}
	// Keep error messages bounded.
	const max = 500
	if len(msg) > max {
		msg = msg[:max] + "…"
	}
	return " (stderr: " + msg + ")"
}

// piEvent is a single record from pi's JSON event stream (JSON mode).
// Only the fields bubbles needs are decoded; unknown records are ignored.
type piEvent struct {
	Type    string           `json:"type"`
	Aborted bool             `json:"aborted,omitempty"`
	Message json.RawMessage  `json:"message,omitempty"`
	Update  *piMessageUpdate `json:"assistantMessageEvent,omitempty"`

	// Tool execution lifecycle fields (tool_execution_start / _end).
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

// piMessageUpdate is the delta-only assistant stream event nested in message_update.
type piMessageUpdate struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta,omitempty"`
	Content      string `json:"content,omitempty"`
}

// piMessage is the subset of pi's AgentMessage bubbles consumes.
type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
}

type piContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// piTextOf extracts the concatenated text of a message's content, which is
// either a plain string (user/system messages) or a list of content blocks.
func piTextOf(content json.RawMessage) string {
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []piContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// piToolSummary returns a short single-line summary of tool arguments for
// display in activity cards. Common descriptive keys are preferred; otherwise
// the compact JSON is truncated.
func piToolSummary(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err == nil {
		for _, key := range []string{"command", "description", "path", "file_path", "prompt", "url"} {
			var s string
			if raw, ok := m[key]; ok && json.Unmarshal(raw, &s) == nil && s != "" {
				return truncateRunes(s, 120)
			}
		}
	}
	return truncateRunes(string(args), 120)
}

// truncateRunes truncates a string to at most n runes, appending "…" if truncated.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// joinBlocks concatenates streaming block buffers in content-index order.
func joinBlocks(blocks map[int]string) string {
	keys := make([]int, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if blocks[k] != "" {
			parts = append(parts, blocks[k])
		}
	}
	return strings.Join(parts, "\n")
}

// piEventMapper converts records from pi's JSON event stream (JSON mode or RPC
// mode session events) into bubbles StreamEvents. Create a fresh mapper per run.
type piEventMapper struct {
	textBlocks  map[int]string
	thinkBlocks map[int]string
	lastText    string
	runErr      error
	Settled     bool
}

func newPiEventMapper() *piEventMapper {
	return &piEventMapper{
		textBlocks:  map[int]string{},
		thinkBlocks: map[int]string{},
	}
}

// Handle decodes one JSONL record and returns the StreamEvents it maps to.
// Unknown records map to nothing.
func (m *piEventMapper) Handle(line []byte) []StreamEvent {
	var ev piEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		slog.Warn("pi stream: failed to unmarshal event", "error", err, "raw", string(line))
		return nil
	}

	switch ev.Type {
	case "session":
		var header struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(line, &header); err == nil && header.ID != "" {
			slog.Info("pi stream: session started", "session_id", header.ID)
			return []StreamEvent{{Type: "system", SessionID: header.ID}}
		}
		return nil

	case "message_start":
		var msg piMessage
		if err := json.Unmarshal(ev.Message, &msg); err == nil && msg.Role == "assistant" {
			m.textBlocks = map[int]string{}
			m.thinkBlocks = map[int]string{}
		}
		return nil

	case "message_update":
		u := ev.Update
		if u == nil {
			return nil
		}
		switch u.Type {
		case "thinking_delta":
			m.thinkBlocks[u.ContentIndex] += u.Delta
		case "thinking_end":
			m.thinkBlocks[u.ContentIndex] = u.Content
			// Emit the full thinking content on block end to match the
			// claude stream's replace-style events and bound card updates.
			if text := joinBlocks(m.thinkBlocks); text != "" {
				return []StreamEvent{{Type: "thinking", Text: text}}
			}
		case "text_delta":
			m.textBlocks[u.ContentIndex] += u.Delta
		case "text_end":
			m.textBlocks[u.ContentIndex] = u.Content
			if text := joinBlocks(m.textBlocks); text != "" {
				return []StreamEvent{{Type: "text", Text: text}}
			}
		}
		return nil

	case "tool_execution_start":
		// tool_execution_update (partial results) is intentionally skipped to
		// bound card updates; start/end gives the full lifecycle.
		return []StreamEvent{{
			Type:      "tool_use",
			ToolName:  ev.ToolName,
			ToolUseID: ev.ToolCallID,
			Text:      piToolSummary(ev.Args),
		}}

	case "tool_execution_end":
		return []StreamEvent{{
			Type:      "tool_result",
			ToolName:  ev.ToolName,
			ToolUseID: ev.ToolCallID,
			IsError:   ev.IsError,
		}}

	case "message_end":
		var msg piMessage
		if err := json.Unmarshal(ev.Message, &msg); err != nil || msg.Role != "assistant" {
			return nil
		}
		m.lastText = piTextOf(msg.Content)
		switch msg.StopReason {
		case "error":
			m.runErr = fmt.Errorf("pi run failed: %s", msg.ErrorMessage)
		case "aborted":
			m.runErr = errors.New("pi run aborted")
		case "length":
			slog.Warn("pi stream: response truncated at token limit")
		}
		return nil

	case "agent_settled":
		m.Settled = true
		if ev.Aborted && m.runErr == nil {
			m.runErr = errors.New("pi run aborted")
		}
		return nil
	}
	return nil
}

// RunErr returns the run-level error observed so far, if any.
func (m *piEventMapper) RunErr() error { return m.runErr }

// Finish builds the terminal "result" event describing the final answer or the
// failure reason. Returns ok=false when there is nothing to show.
func (m *piEventMapper) Finish() (StreamEvent, bool) {
	resultText := m.lastText
	if m.runErr != nil {
		resultText = fmt.Sprintf("⚠️ %v", m.runErr)
		if m.lastText != "" {
			resultText += "\n\n" + m.lastText
		}
	}
	if resultText == "" {
		return StreamEvent{}, false
	}
	return StreamEvent{Type: "result", Text: resultText, IsError: m.runErr != nil}, true
}

// PiStreamWithEvents executes a prompt via the pi CLI in JSON event mode,
// calling the callback with typed events mapped from pi's event stream.
// If resumeSessionID is non-empty, the session is resumed via --session.
func PiStreamWithEvents(ctx context.Context, piPath, prompt, workDir, resumeSessionID, model string, cb EventCallback) error {
	args := append(piArgs("json", model, resumeSessionID), "--", prompt)
	if resumeSessionID != "" {
		slog.Info("pi stream: resuming session", "session_id", resumeSessionID)
	}

	cmd := exec.CommandContext(ctx, piPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stderr stderrBuffer
	cmd.Stderr = &stderr

	slog.Info("pi stream: starting process",
		"path", piPath,
		"args", args,
		"prompt_len", len(prompt),
		"dir", workDir,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pi command: %w", err)
	}

	slog.Info("pi stream: process started", "pid", cmd.Process.Pid)

	var (
		callbackErr error
		mapper      = newPiEventMapper()
	)

	sendEvent := func(event StreamEvent) error {
		if callbackErr != nil {
			return nil
		}
		if err := cb(event); err != nil {
			callbackErr = err
			slog.Error("pi stream: event callback error", "type", event.Type, "error", err)
			// Kill the pi process on callback error to prevent resource leak
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return err
		}
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 初始 64KB，最大 10MB

scan:
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		slog.Debug("pi stream: raw message", "message", line)

		for _, ev := range mapper.Handle([]byte(line)) {
			if err := sendEvent(ev); err != nil {
				break scan
			}
		}
	}

	slog.Info("pi stream: finished", "settled", mapper.Settled, "run_error", mapper.RunErr())

	waitErr := cmd.Wait()

	if callbackErr != nil {
		return fmt.Errorf("stream callback error: %w", callbackErr)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("read pi stream: %w", err)
	}

	// Surface the outcome as a "result" event so cards show the final answer
	// or the failure reason.
	if ev, ok := mapper.Finish(); ok {
		if err := sendEvent(ev); err != nil {
			return fmt.Errorf("stream callback error: %w", err)
		}
	}

	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("pi command canceled: %w", ctx.Err())
		}
		return fmt.Errorf("pi command failed: %w%s", waitErr, stderr.suffix())
	}
	if runErr := mapper.RunErr(); runErr != nil {
		return runErr
	}
	if !mapper.Settled {
		return fmt.Errorf("pi stream ended before agent_settled%s", stderr.suffix())
	}
	return nil
}

// PiStreamWithEventsTimeout executes PiStreamWithEvents with a default 30-minute timeout.
func PiStreamWithEventsTimeout(piPath, prompt, workDir, resumeSessionID, model string, cb EventCallback) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return PiStreamWithEvents(ctx, piPath, prompt, workDir, resumeSessionID, model, cb)
}
