package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Supported agent kinds.
const (
	KindClaude = "claude"
	KindPi     = "pi"
)

// DefaultTimeout is the default timeout for a single agent run.
const DefaultTimeout = 30 * time.Minute

// Result is the outcome of a non-interactive agent run.
type Result struct {
	Output string
	Error  error
}

// StreamEvent represents a typed event from an agent's streaming output.
type StreamEvent struct {
	Type       string // "thinking" | "text" | "tool_use" | "tool_result" | "result" | "system"
	Text       string
	SessionID  string // populated on "system" events
	ToolName   string
	ToolUseID  string
	RawContent json.RawMessage
	IsError    bool
}

// EventCallback is called for each typed event from an agent's streaming output.
type EventCallback func(event StreamEvent) error

// Client executes prompts via an agent CLI (Claude Code or pi).
type Client struct {
	kind  string
	path  string
	model string // optional model override (pi only)
}

// NewClient creates a client for the given agent kind ("claude" or "pi").
// An empty kind defaults to "claude"; an empty path defaults to the kind's CLI name.
// The model argument is optional and only honored by pi.
func NewClient(kind, path, model string) *Client {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != KindPi {
		kind = KindClaude
	}
	if path == "" {
		path = kind
	}
	return &Client{kind: kind, path: path, model: model}
}

// Kind returns the configured agent kind ("claude" or "pi").
func (c *Client) Kind() string { return c.kind }

// DisplayName returns a human-readable agent name for logs and cards.
func (c *Client) DisplayName() string {
	if c.kind == KindPi {
		return "Pi"
	}
	return "Claude Code"
}

// Run executes a prompt non-interactively and returns the combined output.
func (c *Client) Run(ctx context.Context, prompt, workDir string) Result {
	if c.kind == KindPi {
		return Pi(ctx, c.path, prompt, workDir, c.model)
	}
	return Claude(ctx, c.path, prompt, workDir)
}

// RunWithTimeout executes a prompt with a default 30-minute timeout.
func (c *Client) RunWithTimeout(prompt, workDir string) Result {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return c.Run(ctx, prompt, workDir)
}

// StreamWithEvents executes a prompt in streaming mode, calling cb with typed
// events. If resumeSessionID is non-empty, the previous session is resumed.
func (c *Client) StreamWithEvents(ctx context.Context, prompt, workDir, resumeSessionID string, cb EventCallback) error {
	if c.kind == KindPi {
		return PiStreamWithEvents(ctx, c.path, prompt, workDir, resumeSessionID, c.model, cb)
	}
	return ClaudeStreamWithEvents(ctx, c.path, prompt, workDir, resumeSessionID, cb)
}

// StreamWithEventsTimeout executes StreamWithEvents with a default 30-minute timeout.
func (c *Client) StreamWithEventsTimeout(prompt, workDir, resumeSessionID string, cb EventCallback) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return c.StreamWithEvents(ctx, prompt, workDir, resumeSessionID, cb)
}
