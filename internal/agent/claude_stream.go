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

// StreamCallback is called for each formatted text segment from Claude's streaming output.
type StreamCallback func(chunk string) error

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
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// ClaudeStream executes a prompt via the claude CLI in streaming mode.
//
// Each SDK event (text, tool_use, tool_result, thinking) is formatted as readable
// text and immediately flushed to the callback. The Feishu SDK's internal throttle
// (100ms via StreamThrottleMs) handles rate limiting to the Feishu API, so we don't
// need an outer buffer. This ensures each logical segment appears separately in Feishu.
func ClaudeStream(ctx context.Context, claudePath, prompt, workDir string, cb StreamCallback) error {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
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

	// Write user message via stdin in a goroutine to avoid deadlock.
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

	// Guard: close stdout on context cancellation so scanner.Scan() unblocks.
	go func() {
		<-ctx.Done()
		closeStdin()
		_ = stdout.Close()
	}()

	// Track callback errors across immediate flushes
	var mu sync.Mutex
	var callbackErr error
	var sessionID string
	var totalFlushed int

	// sendAndFlush formats a text segment and immediately calls the callback.
	// No buffering — each SDK event becomes a separate Feishu update.
	sendAndFlush := func(text string) {
		if text == "" {
			return
		}
		mu.Lock()
		if callbackErr != nil {
			mu.Unlock()
			return
		}
		mu.Unlock()

		totalFlushed++
		slog.Info("claude stream: flushing segment", "segment#", totalFlushed, "bytes", len(text))
		if err := cb(text); err != nil {
			mu.Lock()
			callbackErr = err
			mu.Unlock()
			slog.Error("claude stream: callback error", "segment#", totalFlushed, "error", err)
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var eventCount int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var msg claudeSDKMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		eventCount++

		switch msg.Type {
		case "assistant":
			var content claudeMessageContent
			if err := json.Unmarshal(msg.Message, &content); err != nil {
				continue
			}
			for _, block := range content.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						sendAndFlush(block.Text)
					}
				case "thinking":
					if block.Text != "" {
						sendAndFlush(fmt.Sprintf("\n💭 *思考中...*\n> %s\n\n", strings.ReplaceAll(block.Text, "\n", "\n> ")))
					}
				case "tool_use":
					var input map[string]any
					if block.Input != nil {
						_ = json.Unmarshal(block.Input, &input)
					}
					toolText := fmt.Sprintf("\n🔧 **调用工具: %s**\n", block.Name)
					if input != nil {
						toolText += formatToolInput(block.Name, input)
					}
					toolText += "\n"
					sendAndFlush(toolText)
					slog.Info("claude stream: tool_use", "tool", block.Name, "call_id", block.ID)
				}
			}

		case "user":
			var content claudeMessageContent
			if err := json.Unmarshal(msg.Message, &content); err != nil {
				continue
			}
			for _, block := range content.Content {
				if block.Type == "tool_result" {
					resultText := formatToolResult(block.Content)
					sendAndFlush(resultText)
					slog.Info("claude stream: tool_result", "call_id", block.ToolUseID, "raw_bytes", len(block.Content))
				}
			}

		case "system":
			if msg.SessionID != "" && sessionID == "" {
				sessionID = msg.SessionID
				slog.Info("claude stream: session started", "session_id", sessionID)
			}

		case "result":
			slog.Info("claude stream: execution result",
				"is_error", msg.IsError,
				"result_len", len(msg.ResultText),
				"duration_ms", msg.DurationMs,
				"num_turns", msg.NumTurns,
				"total_events", eventCount,
				"total_segments", totalFlushed,
			)
			closeStdin()

		case "log":
			if msg.Log != nil {
				slog.Debug("claude stream: log", "level", msg.Log.Level, "message", msg.Log.Message)
			}

		case "control_request":
			handleControlRequest(msg, stdin)
		}

		// Check for callback error (e.g. Feishu edit limit exceeded)
		mu.Lock()
		err := callbackErr
		mu.Unlock()
		if err != nil {
			slog.Warn("claude stream: stopping due to callback error")
			break
		}
	}

	slog.Info("claude stream: finished",
		"total_events", eventCount,
		"total_segments", totalFlushed,
	)

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

// formatToolResult formats a tool_result's raw content for Feishu display.
// Long results are truncated with head + tail to keep messages readable.
const toolResultMaxLen = 1500

func formatToolResult(raw json.RawMessage) string {
	rawLen := len(raw)
	if rawLen == 0 {
		return "✅ **工具结果** (empty)\n\n"
	}

	text := strings.TrimSpace(string(raw))

	// Try to extract meaningful content from JSON-wrapped results
	// Common patterns: {"type":"text","text":"..."} or plain text
	if strings.HasPrefix(text, "{") {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			if t, ok := obj["text"]; ok {
				var s string
				if json.Unmarshal(t, &s) == nil {
					text = s
				}
			} else if t, ok := obj["output"]; ok {
				var s string
				if json.Unmarshal(t, &s) == nil {
					text = s
				}
			}
		}
	}

	text = strings.TrimSpace(text)

	if len(text) <= toolResultMaxLen {
		return fmt.Sprintf("✅ **工具结果** (%d bytes)\n```\n%s\n```\n\n", rawLen, text)
	}

	// Show head + tail with truncation indicator
	headLen := toolResultMaxLen / 2
	tailLen := toolResultMaxLen / 2
	head := text[:headLen]
	tail := text[len(text)-tailLen:]
	return fmt.Sprintf("✅ **工具结果** (%d bytes, 截断显示)\n```\n%s\n\n... 省略 %d 字符 ...\n\n%s\n```\n\n",
		rawLen, head, len(text)-headLen-tailLen, tail)
}
func formatToolInput(toolName string, input map[string]any) string {
	switch toolName {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			if len(cmd) > 300 {
				cmd = cmd[:300] + "..."
			}
			return fmt.Sprintf("```bash\n%s\n```\n", cmd)
		}
	case "Read":
		if f, ok := input["file_path"].(string); ok {
			return fmt.Sprintf("📄 `%s`\n", f)
		}
	case "Edit":
		file := ""
		if f, ok := input["file_path"].(string); ok {
			file = f
		}
		return fmt.Sprintf("✏️ 编辑 `%s`\n", file)
	case "Write":
		if f, ok := input["file_path"].(string); ok {
			return fmt.Sprintf("📝 写入 `%s`\n", f)
		}
	case "Agent":
		if desc, ok := input["description"].(string); ok {
			return fmt.Sprintf("🤖 %s\n", desc)
		}
	case "EnterPlanMode":
		return "📋 进入计划模式\n"
	case "TaskCreate":
		if subj, ok := input["subject"].(string); ok {
			return fmt.Sprintf("📋 创建任务: %s\n", subj)
		}
	}
	data, _ := json.Marshal(input)
	s := string(data)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return fmt.Sprintf("```\n%s\n```\n", s)
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

// ClaudeStreamWithTimeout executes ClaudeStream with a default 30-minute timeout.
func ClaudeStreamWithTimeout(claudePath, prompt, workDir string, cb StreamCallback) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return ClaudeStream(ctx, claudePath, prompt, workDir, cb)
}
