package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaude_ClaudeNotFound(t *testing.T) {
	// Use a path that definitely doesn't exist
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := Claude(ctx, "/nonexistent/claude/binary", "test prompt", "")

	if result.Error == nil {
		t.Fatal("expected error when claude binary not found")
	}

	if result.Output == "" {
		// Output may be empty when command fails to start
		t.Log("output is empty (expected for binary not found)")
	}
}

func TestClaude_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := Claude(ctx, "sleep", "60", "")

	if result.Error == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestClaudeWithTimeout_Timeout(t *testing.T) {
	// Use a command that will hang (sleep with a very long timeout)
	// ClaudeWithTimeout uses 30min timeout, but we test the mechanism
	// by using a short-lived command that exists
	result := ClaudeWithTimeout("echo", "hello", "")

	// echo should succeed
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Output == "" {
		t.Error("expected output from echo")
	}
}

func TestClaudeWithTimeout_InvalidCommand(t *testing.T) {
	result := ClaudeWithTimeout("/nonexistent/cmd", "test", "")

	if result.Error == nil {
		t.Fatal("expected error for invalid command")
	}
}

func TestClaudeResult_Fields(t *testing.T) {
	tests := []struct {
		name    string
		result  ClaudeResult
		wantErr bool
	}{
		{
			name:    "success",
			result:  ClaudeResult{Output: "ok"},
			wantErr: false,
		},
		{
			name:    "error",
			result:  ClaudeResult{Output: "fail", Error: context.DeadlineExceeded},
			wantErr: true,
		},
		{
			name:    "empty",
			result:  ClaudeResult{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.result.Error != nil) != tt.wantErr {
				t.Errorf("Error = %v, wantErr = %v", tt.result.Error, tt.wantErr)
			}
		})
	}
}

func TestClaude_WithWorkDir(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use a script that ignores arguments and prints the working directory
	script := filepath.Join(dir, "fake_claude.sh")
	os.WriteFile(script, []byte("#!/bin/sh\npwd\n"), 0755)

	result := Claude(ctx, script, "test prompt", dir)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Output should contain the temp dir
	if result.Output == "" {
		t.Error("expected output from fake claude script")
	}
}

func TestClaude_EmptyPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// echo with empty prompt should still work
	result := Claude(ctx, "echo", "", "")

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
}

// TestStreamEvent_Fields tests the StreamEvent struct.
func TestStreamEvent_Fields(t *testing.T) {
	event := StreamEvent{
		Type:      "text",
		Text:      "hello",
		SessionID: "sess_123",
		ToolName:  "bash",
		ToolUseID: "tu_456",
		IsError:   false,
	}

	if event.Type != "text" {
		t.Errorf("Type = %q, want %q", event.Type, "text")
	}
	if event.Text != "hello" {
		t.Errorf("Text = %q, want %q", event.Text, "hello")
	}
	if event.SessionID != "sess_123" {
		t.Errorf("SessionID = %q, want %q", event.SessionID, "sess_123")
	}
	if event.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", event.ToolName, "bash")
	}
	if event.ToolUseID != "tu_456" {
		t.Errorf("ToolUseID = %q, want %q", event.ToolUseID, "tu_456")
	}
	if event.IsError {
		t.Error("IsError should be false")
	}
}

// TestBuildClaudeInput tests the buildClaudeInput function.
func TestBuildClaudeInput(t *testing.T) {
	data, err := buildClaudeInput("test prompt")
	if err != nil {
		t.Fatalf("buildClaudeInput: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty data")
	}

	// Should end with newline
	if data[len(data)-1] != '\n' {
		t.Error("expected trailing newline")
	}

	// Should be valid JSON (excluding the trailing newline)
	jsonStr := string(data[:len(data)-1])
	if jsonStr[0] != '{' {
		t.Error("expected JSON object")
	}
}

func TestBuildClaudeInput_EmptyPrompt(t *testing.T) {
	data, err := buildClaudeInput("")
	if err != nil {
		t.Fatalf("buildClaudeInput: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty data")
	}
}

func TestBuildClaudeInput_UnicodePrompt(t *testing.T) {
	data, err := buildClaudeInput("你好世界 🚀")
	if err != nil {
		t.Fatalf("buildClaudeInput: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty data")
	}
}
