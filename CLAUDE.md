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
JSON-RPC over Unix socket at `~/.bubbles/bubblesd.sock`. One request per connection — no multiplexing or keepalive. Sixteen methods defined in `internal/ipc/protocol.go` (`task.create`, `task.list`, `task.get`, `task.delete`, `task.pause`, `task.resume`, `task.run`, `task.logs`, `daemon.status`, `session.stats`, `session.export`, `feishu.notify`, `holding.list`, `holding.get`, `holding.upsert`, `holding.delete`).

### Two Execution Paths

Both paths run through the agent abstraction in `internal/agent/` (`agent.Client`), which selects a backend from config: `agent: claude` (default) or `agent: pi`.

1. **Scheduled tasks** — Cron or one-time tasks created via CLI. Executor spawns the agent non-interactively (`claude --print -p <prompt> --dangerously-skip-permissions` or `pi --print --approve -- <prompt>`), capturing all output and writing it to the SQLite execution log. Fire-and-forget: the CLI gets an immediate response, execution happens in a background goroutine.
2. **Feishu interactive chat** — Messages handled by `internal/feishu/channel.go` bypass the task system entirely. They use streaming mode emitting shared `agent.StreamEvent` values (system/thinking/text/tool_use/tool_result/result) through a callback that updates streaming Feishu cards. Two transports exist for pi:
   - **RPC transport** (agent `pi`) — `internal/agent/rpc.go` keeps one long-lived `pi --mode rpc` subprocess per chat session (`internal/agent/rpc_manager.go` pools them with idle reaping). A message arriving mid-run is delivered via `steer`/`follow_up` (see `pi_rpc_behavior`) instead of queueing; `/stop` maps to `abort`. The `agent_settled` event ends a run.
   - **Spawn transport** (agent `claude`) — one-shot `claude --print` per message with `--resume` continuation (`claude_stream.go`). Messages in the same chat session are serialized via a semaphore. (`pi_stream.go` keeps the one-shot `pi --mode json` transport used by one-off streams.)
   The claude backend auto-approves tool permissions via `control_response`; pi runs tools without per-call prompts and trusts project resources via `--approve`. Session IDs captured from stream events allow resuming the conversation.

### Session Operations
`bubbles session stats <session-id>` and `bubbles session export <session-id> -o file.html` run out-of-band pi session queries through the daemon (`internal/agent/session_ops.go` starts a short-lived `pi --mode rpc --session <id>` to answer `get_session_stats` / `export_html`).

### Pi Extension
`extensions/bubbles-pi/` is a pi extension (TypeScript, loaded by pi via jiti) exposing `feishu_notify`, `bubbles_task`, `bubbles_status`, `bubbles_holding` tools and a `/bubbles` command. Tools reach bubblesd over the IPC unix socket (including `feishu.notify`, backed by `FeishuNotifier.NotifyText`). Its `tool_call` approval gate turns pi extension-UI confirm dialogs into Feishu cards via the RPC `extension_ui_request/response` subprotocol (`internal/agent/ui.go` defines the bridge contract; `internal/feishu/ui_bridge.go` renders confirm/select dialogs as cards with callback buttons). See `extensions/bubbles-pi/README.md`.

### Conversation Branching
Final answer cards carry a **🔄 重新回答** button and the `/refork` command lists earlier questions; both use pi's `fork` / `get_fork_messages` RPC commands (`RPCSession.Fork`) to re-run a prompt from a branch point, preserving prior answers in the session tree (`internal/feishu/channel.go`: `runRefork`, `HandleReforkCommand`).

### Data Layer
SQLite via `mattn/go-sqlite3` (requires CGO). WAL mode, 5s busy timeout. Auto-migrates `tasks`, `execution_logs` and `holdings` tables on startup. Data directory: `~/.bubbles/`.

Holdings (portfolio positions) live in the `holdings` table (`internal/store/holdings.go`, ticker PK with qty/cost/state/hypothesis/stop_loss/target/review_date/decision). Three access paths: CLI `bubbles holding list|get|set|rm` (`internal/cli/holding.go`), Feishu `/holding` command (`internal/feishu/holding.go`, `/holding 修改 HK.01810 成本=31.3` style key=value edits, destructive deletes need `确认`), and the pi `bubbles_holding` tool. `holding.upsert` is a partial update (omitted fields keep stored values; `name` required on create).

Task IDs are time-based: `task_YYYYMMDDHHmmss` (potential collision if two tasks created within the same second). ExecutionLog IDs use UUIDs.

### Configuration
`~/.bubbles/config.yaml` (YAML). Env vars override: `FEISHU_APP_ID`, `FEISHU_APP_SECRET`, `BUBBLES_AGENT`, `CLAUDE_PATH`, `PI_PATH`, `BUBBLES_PI_MODEL`, `BUBBLES_DATA_DIR`, `BUBBLES_PI_RPC_BEHAVIOR`. Agent selection: `agent: claude|pi` with per-agent CLI paths (`claude_path`, `pi_path`) and an optional `pi_model` (passed to `pi --model`). pi chat tuning: `pi_rpc_behavior` (`steer` | `follow_up`), `pi_rpc_idle_minutes` (RPC session idle TTL, default 30). See `internal/config/config.go`.

### Key Dependencies
- `robfig/cron/v3` — Cron scheduling
- `spf13/cobra` — CLI framework
- `mattn/go-sqlite3` — SQLite (CGO required)
- `larksuite/oapi-sdk-go/v3` — Feishu/Lark bot SDK（公共代理依赖，v3.9.5；无本地 replace）
