package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StreamEvent represents a typed event from Claude's streaming output.
type StreamEvent struct {
	Type        string // "thinking" | "text" | "tool_use" | "tool_result" | "result" | "system"
	Text        string
	SessionID   string // populated on "system" events
	ToolName    string
	ToolUseID   string
	RawContent  json.RawMessage
	IsError     bool
}

// EventCallback is called for each typed event from Claude's streaming output.
type EventCallback func(event StreamEvent) error

// claudeSDKMessage represents a single event in Claude's stream-json output.
type claudeSDKMessage struct {
	Type       string          `json:"type"`
	Message    json.RawMessage `json:"message,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	ResultText string          `json:"result,omitempty"`
	Model      string          `json:"model,omitempty"`
	DurationMs float64         `json:"duration_ms,omitempty"`
	NumTurns   int             `json:"num_turns,omitempty"`

	Log *struct {
		Level   string `json:"level"`
		Message string `json:"message"`
	} `json:"log,omitempty"`

	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`
}

type claudeControlRequestPayload struct {
	Subtype  string          `json:"subtype"`
	ToolName string          `json:"tool_name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type claudeMessageContent struct {
	Role    string               `json:"role"`
	Model   string               `json:"model"`
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

func handleControlRequest(msg claudeSDKMessage, stdin io.Writer) {
	var req claudeControlRequestPayload
	if err := json.Unmarshal(msg.Request, &req); err != nil {
		return
	}

	slog.Info("claude stream: control_request", "subtype", req.Subtype, "tool", req.ToolName, "request_id", msg.RequestID)

	var inputMap map[string]any
	if req.Input != nil {
		_ = json.Unmarshal(req.Input, &inputMap)
	}
	if inputMap == nil {
		inputMap = map[string]any{}
	}

	response := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": msg.RequestID,
			"response": map[string]any{
				"behavior":     "allow",
				"updatedInput": inputMap,
			},
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if _, err := stdin.Write(data); err != nil {
		slog.Warn("claude stream: failed to write control response", "error", err)
	}
}

func buildClaudeInput(prompt string) ([]byte, error) {
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]string{
				{
					"type": "text",
					"text": prompt,
				},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal claude input: %w", err)
	}
	return append(data, '\n'), nil
}

// ClaudeStreamWithEvents executes a prompt via the claude CLI in streaming mode,
// calling the callback with typed events for each content block.
// If resumeSessionID is non-empty, the session is resumed via --resume.
func ClaudeStreamWithEvents(ctx context.Context, claudePath, prompt, workDir, resumeSessionID string, cb EventCallback) error {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
	}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
		slog.Info("claude stream: resuming session", "session_id", resumeSessionID)
	}

	cmd := exec.CommandContext(ctx, claudePath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if os.Getuid() == 0 {
		cmd.Env = append(os.Environ(), "IS_SANDBOX=1")
	}

	slog.Info("claude stream: starting process",
		"path", claudePath,
		"args", args,
		"prompt_len", len(prompt),
		"dir", workDir,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	var closeStdinOnce sync.Once
	closeStdin := func() { closeStdinOnce.Do(func() { _ = stdin.Close() }) }

	if err := cmd.Start(); err != nil {
		closeStdin()
		return fmt.Errorf("start claude command: %w", err)
	}

	slog.Info("claude stream: process started", "pid", cmd.Process.Pid)

	writeDone := make(chan error, 1)
	go func() {
		data, err := buildClaudeInput(prompt)
		if err != nil {
			writeDone <- err
			closeStdin()
			return
		}
		if _, err := stdin.Write(data); err != nil {
			writeDone <- err
			closeStdin()
			return
		}
		writeDone <- nil
	}()

	go func() {
		<-ctx.Done()
		closeStdin()
		_ = stdout.Close()
	}()

	var mu sync.Mutex
	var callbackErr error

	sendEvent := func(event StreamEvent) error {
		mu.Lock()
		if callbackErr != nil {
			mu.Unlock()
			return nil
		}
		mu.Unlock()

		if err := cb(event); err != nil {
			mu.Lock()
			callbackErr = err
			mu.Unlock()
			slog.Error("claude stream: event callback error", "type", event.Type, "error", err)
			// Kill the Claude process on callback error to prevent resource leak
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return err
		}
		return nil
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 初始 64KB，最大 10MB

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		slog.Info("claude stream: raw message", "message", line)

		var msg claudeSDKMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			slog.Warn("claude stream: failed to unmarshal message", "error", err, "raw", line)
			continue
		}

		switch msg.Type {
		case "assistant":
			var content claudeMessageContent
			if err := json.Unmarshal(msg.Message, &content); err != nil {
				slog.Debug("claude stream: failed to unmarshal assistant message", "error", err)
				continue
			}
			slog.Debug("claude stream: assistant event", "content_blocks", len(content.Content), "model", content.Model)
			for _, block := range content.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						if err := sendEvent(StreamEvent{Type: "text", Text: block.Text}); err != nil {
							closeStdin()
							return err
						}
					}
				case "thinking":
					if block.Thinking != "" {
						if err := sendEvent(StreamEvent{Type: "thinking", Text: block.Thinking}); err != nil {
							closeStdin()
							return err
						}
					}
				}
			}

		case "user":
			// tool_result events are skipped (tool calls not displayed)

		case "system":
			if msg.SessionID != "" {
				slog.Info("claude stream: session started", "session_id", msg.SessionID)
				if err := sendEvent(StreamEvent{Type: "system", SessionID: msg.SessionID}); err != nil {
					closeStdin()
					return err
				}
			}

		case "result":
			slog.Info("claude stream: execution result",
				"is_error", msg.IsError,
				"result_len", len(msg.ResultText),
				"duration_ms", msg.DurationMs,
			)
			if msg.ResultText != "" {
				if err := sendEvent(StreamEvent{Type: "result", Text: msg.ResultText}); err != nil {
					closeStdin()
					return err
				}
			}
			closeStdin()

		case "log":
			if msg.Log != nil {
				slog.Debug("claude stream: log", "level", msg.Log.Level, "message", msg.Log.Message)
			}

		case "control_request":
			handleControlRequest(msg, stdin)
		}

		mu.Lock()
		err := callbackErr
		mu.Unlock()
		if err != nil {
			break
		}
	}

	slog.Info("claude stream: finished")

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("read claude stream: %w", err)
	}

	mu.Lock()
	err = callbackErr
	mu.Unlock()
	if err != nil {
		return fmt.Errorf("stream callback error: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		writeErr := <-writeDone
		if writeErr != nil {
			return fmt.Errorf("write input: %w (cmd: %v)", writeErr, err)
		}
		return fmt.Errorf("claude command failed: %w", err)
	}

	return nil
}

// ClaudeStreamWithEventsTimeout executes ClaudeStreamWithEvents with a default 30-minute timeout.
func ClaudeStreamWithEventsTimeout(claudePath, prompt, workDir, resumeSessionID string, cb EventCallback) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return ClaudeStreamWithEvents(ctx, claudePath, prompt, workDir, resumeSessionID, cb)
}
