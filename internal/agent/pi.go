package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
)

// piArgs builds the common pi CLI arguments shared by one-shot and streaming runs.
func piArgs(mode, model, resumeSessionID string) []string {
	args := []string{}
	switch mode {
	case "print":
		// Print mode writes the final assistant text to stdout and exits.
		args = append(args, "--print")
	case "":
		// keep defaults
	default:
		args = append(args, "--mode", mode)
	}
	// Trust project-local files: non-interactive modes cannot show the trust
	// prompt, and scheduled/chat tasks run inside a configured work dir.
	args = append(args, "--approve")
	if model != "" {
		args = append(args, "--model", model)
	}
	if resumeSessionID != "" {
		args = append(args, "--session", resumeSessionID)
	}
	return args
}

// Pi executes a prompt via the pi CLI in print mode. The final assistant text
// is returned as Output; diagnostics from pi go to stderr and are surfaced on
// error.
func Pi(ctx context.Context, piPath, prompt, workDir, model string) Result {
	args := append(piArgs("print", model, ""), "--", prompt)

	cmd := exec.CommandContext(ctx, piPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	slog.Debug("running pi command", "args", args, "dir", cmd.Dir)

	var stderr stderrBuffer
	cmd.Stderr = &stderr

	// Print mode reserves stdout for the final assistant text.
	output, err := cmd.Output()
	if err != nil {
		return Result{
			Output: string(output),
			Error:  fmt.Errorf("pi command failed: %w%s", err, stderr.suffix()),
		}
	}

	return Result{
		Output: string(output),
	}
}

// PiWithTimeout executes a pi prompt with a default 30-minute timeout.
func PiWithTimeout(piPath, prompt, workDir, model string) Result {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return Pi(ctx, piPath, prompt, workDir, model)
}
