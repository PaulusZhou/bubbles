package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

type ClaudeResult struct {
	Output string
	Error  error
}

// Claude executes a prompt via the claude CLI in non-interactive mode.
func Claude(ctx context.Context, claudePath, prompt, workDir string) ClaudeResult {
	args := []string{"--print", "-p", prompt, "--dangerously-skip-permissions"}

	cmd := exec.CommandContext(ctx, claudePath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	// root user needs IS_SANDBOX=1
	if os.Getuid() == 0 {
		cmd.Env = append(os.Environ(), "IS_SANDBOX=1")
	}

	slog.Debug("running claude command", "args", args, "dir", cmd.Dir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return ClaudeResult{
			Output: string(output),
			Error:  fmt.Errorf("claude command failed: %w", err),
		}
	}

	return ClaudeResult{
		Output: string(output),
	}
}

// ClaudeWithTimeout executes a prompt with a default 30-minute timeout.
func ClaudeWithTimeout(claudePath, prompt, workDir string) ClaudeResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return Claude(ctx, claudePath, prompt, workDir)
}
