package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakePi writes a shell script standing in for the pi CLI and returns its path.
func writeFakePi(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake_pi.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	return path
}

func TestPi_BinaryNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := Pi(ctx, "/nonexistent/pi/binary", "test prompt", "", "")

	if result.Error == nil {
		t.Fatal("expected error when pi binary not found")
	}
}

func TestPi_WithWorkDir(t *testing.T) {
	dir := t.TempDir()
	script := writeFakePi(t, "pwd\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := Pi(ctx, script, "test prompt", dir, "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !strings.Contains(result.Output, filepath.Base(dir)) {
		t.Errorf("output %q should contain work dir base name", result.Output)
	}
}

func TestPi_Args(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("BUBBLES_TEST_ARGS_FILE", argsFile)
	script := writeFakePi(t, `printf '%s\n' "$@" > "$BUBBLES_TEST_ARGS_FILE"`+"\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := Pi(ctx, script, "do things", "", "some/model")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"--print", "--approve", "--model", "some/model", "--", "do things"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestPi_ErrorSurfacesStderr(t *testing.T) {
	script := writeFakePi(t, "echo 'kaboom' >&2\nexit 1\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := Pi(ctx, script, "prompt", "", "")
	if result.Error == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(result.Error.Error(), "kaboom") {
		t.Errorf("error %q should contain stderr content", result.Error)
	}
}

func TestPiWithTimeout_Success(t *testing.T) {
	script := writeFakePi(t, "echo hello\n")

	result := PiWithTimeout(script, "prompt", "", "")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if strings.TrimSpace(result.Output) != "hello" {
		t.Errorf("output = %q, want %q", result.Output, "hello")
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		kind        string
		wantKind    string
		wantDisplay string
	}{
		{"", KindClaude, "Claude Code"},
		{"claude", KindClaude, "Claude Code"},
		{"pi", KindPi, "Pi"},
		{"PI", KindPi, "Pi"},
		{"unknown", KindClaude, "Claude Code"},
	}
	for _, tt := range tests {
		c := NewClient(tt.kind, "", "")
		if c.Kind() != tt.wantKind {
			t.Errorf("NewClient(%q).Kind() = %q, want %q", tt.kind, c.Kind(), tt.wantKind)
		}
		if c.DisplayName() != tt.wantDisplay {
			t.Errorf("NewClient(%q).DisplayName() = %q, want %q", tt.kind, c.DisplayName(), tt.wantDisplay)
		}
	}

	if c := NewClient("pi", "/usr/bin/pi", "m"); c.kind != "pi" || c.path != "/usr/bin/pi" || c.model != "m" {
		t.Errorf("NewClient with explicit fields = %+v", c)
	}
}
