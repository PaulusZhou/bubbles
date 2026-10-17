# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run Commands

- `make build` — Build both `bin/bubbles` and `bin/bubblesd` (requires CGO_ENABLED=1 for SQLite)
- `make install` — Build and copy binaries to `/usr/local/bin/`
- `make run` — Build and run `bin/bubblesd` directly
- `make test` — Run `go test ./...` (no tests exist yet)
- `make clean` — Remove `bin/`, daemon socket, and PID file
- `make tidy` — Run `go mod tidy`

To run a single test: `go test ./internal/store/ -run TestName`

## Architecture

Bubbles is a scheduled task runner that invokes Claude Code on a cron or one-time schedule. It uses a classic CLI + daemon architecture communicating over Unix domain sockets.

### Two Binaries
- **`bubbles`** (cmd/bubbles/) — Thin Cobra CLI client. Sends IPC requests and exits.
- **`bubblesd`** (cmd/bubblesd/) — Background daemon. Runs the scheduler, IPC server, and optionally Feishu channel.

The CLI starts the daemon via `syscall.ForkExec` (Linux/macOS only).

### IPC Protocol
JSON-RPC over Unix socket at `~/.bubbles/bubblesd.sock`. One request per connection — no multiplexing or keepalive. Nine methods defined in `internal/ipc/protocol.go` (`task.create`, `task.list`, `task.get`, `task.delete`, `task.pause`, `task.resume`, `task.run`, `task.logs`, `daemon.status`).

### Two Execution Paths

Both paths run through the agent abstraction in `internal/agent/` (`agent.Client`), which selects a backend from config: `agent: claude` (default) or `agent: pi`.

1. **Scheduled tasks** — Cron or one-time tasks created via CLI. Executor spawns the agent non-interactively (`claude --print -p <prompt> --dangerously-skip-permissions` or `pi --print --approve -- <prompt>`), capturing all output and writing it to the SQLite execution log. Fire-and-forget: the CLI gets an immediate response, execution happens in a background goroutine.
2. **Feishu interactive chat** — Messages handled by `internal/feishu/channel.go` bypass the task system entirely. They use streaming mode (`ClaudeStreamWithEvents` for claude, `PiStreamWithEvents` for pi over `--mode json` JSONL events), emitting shared `agent.StreamEvent` values (system/thinking/text/result) through a callback that updates streaming Feishu cards. The claude backend auto-approves tool permissions via `control_response`; pi runs tools without per-call prompts and trusts project resources via `--approve`. Session IDs captured from stream events allow resuming the conversation (`claude --resume` / `pi --session`).

### Data Layer
SQLite via `mattn/go-sqlite3` (requires CGO). WAL mode, 5s busy timeout. Auto-migrates `tasks` and `execution_logs` tables on startup. Data directory: `~/.bubbles/`.

Task IDs are time-based: `task_YYYYMMDDHHmmss` (potential collision if two tasks created within the same second). ExecutionLog IDs use UUIDs.

### Configuration
`~/.bubbles/config.yaml` (YAML). Env vars override: `FEISHU_APP_ID`, `FEISHU_APP_SECRET`, `BUBBLES_AGENT`, `CLAUDE_PATH`, `PI_PATH`, `BUBBLES_PI_MODEL`, `BUBBLES_DATA_DIR`. Agent selection: `agent: claude|pi` with per-agent CLI paths (`claude_path`, `pi_path`) and an optional `pi_model` (passed to `pi --model`). See `internal/config/config.go`.

### Key Dependencies
- `robfig/cron/v3` — Cron scheduling
- `spf13/cobra` — CLI framework
- `mattn/go-sqlite3` — SQLite (CGO required)
- `larksuite/oapi-sdk-go/v3` — Feishu/Lark bot SDK (**local replace directive** in go.mod points to `/Users/pauluszhou/Projects/oapi-sdk-go`; building elsewhere requires adjusting this)
