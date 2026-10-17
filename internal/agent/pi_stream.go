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
		runErr      error
		settled     bool
		lastText    string
		textBlocks  = map[int]string{}
		thinkBlocks = map[int]string{}
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

		var ev piEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			slog.Warn("pi stream: failed to unmarshal event", "error", err, "raw", line)
			continue
		}

		switch ev.Type {
		case "session":
			var header struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(line), &header); err == nil && header.ID != "" {
				slog.Info("pi stream: session started", "session_id", header.ID)
				if err := sendEvent(StreamEvent{Type: "system", SessionID: header.ID}); err != nil {
					break scan
				}
			}

		case "message_start":
			var msg piMessage
			if err := json.Unmarshal(ev.Message, &msg); err == nil && msg.Role == "assistant" {
				textBlocks = map[int]string{}
				thinkBlocks = map[int]string{}
			}

		case "message_update":
			u := ev.Update
			if u == nil {
				continue
			}
			switch u.Type {
			case "thinking_delta":
				thinkBlocks[u.ContentIndex] += u.Delta
				continue
			case "thinking_end":
				thinkBlocks[u.ContentIndex] = u.Content
				// Emit the full thinking content on block end to match the
				// claude stream's replace-style events and bound card updates.
				if text := joinBlocks(thinkBlocks); text != "" {
					if err := sendEvent(StreamEvent{Type: "thinking", Text: text}); err != nil {
						break scan
					}
				}
			case "text_delta":
				textBlocks[u.ContentIndex] += u.Delta
				continue
			case "text_end":
				textBlocks[u.ContentIndex] = u.Content
				if text := joinBlocks(textBlocks); text != "" {
					if err := sendEvent(StreamEvent{Type: "text", Text: text}); err != nil {
						break scan
					}
				}
			}

		case "message_end":
			var msg piMessage
			if err := json.Unmarshal(ev.Message, &msg); err != nil || msg.Role != "assistant" {
				continue
			}
			lastText = piTextOf(msg.Content)
			switch msg.StopReason {
			case "error":
				runErr = fmt.Errorf("pi run failed: %s", msg.ErrorMessage)
			case "aborted":
				runErr = errors.New("pi run aborted")
			case "length":
				slog.Warn("pi stream: response truncated at token limit")
			}

		case "agent_settled":
			settled = true
			if ev.Aborted && runErr == nil {
				runErr = errors.New("pi run aborted")
			}
		}

		if callbackErr != nil {
			break
		}
	}

	slog.Info("pi stream: finished", "settled", settled, "run_error", runErr)

	waitErr := cmd.Wait()

	if callbackErr != nil {
		return fmt.Errorf("stream callback error: %w", callbackErr)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("read pi stream: %w", err)
	}

	// Surface the outcome as a "result" event so cards show the final answer
	// or the failure reason.
	resultText := lastText
	if runErr != nil {
		resultText = fmt.Sprintf("⚠️ %v", runErr)
		if lastText != "" {
			resultText += "\n\n" + lastText
		}
	}
	if resultText != "" {
		if err := sendEvent(StreamEvent{Type: "result", Text: resultText, IsError: runErr != nil}); err != nil {
			return fmt.Errorf("stream callback error: %w", err)
		}
	}

	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("pi command canceled: %w", ctx.Err())
		}
		return fmt.Errorf("pi command failed: %w%s", waitErr, stderr.suffix())
	}
	if runErr != nil {
		return runErr
	}
	if !settled {
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
