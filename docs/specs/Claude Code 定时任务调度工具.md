# Bubbles - Claude Code 定时任务调度工具

## Context
用户需要一个可以定时调用 Claude Code 执行任务的工具。项目从零开始，使用 Go 实现。核心需求：daemon 常驻进程负责调度执行，CLI 工具负责管理任务。

## 架构设计

```
┌─────────┐  Unix Socket  ┌──────────────┐  exec  ┌─────────────┐
│  bubbles │ ◄──────────► │  bubblesd    │ ─────► │ claude code │
│  (CLI)   │   IPC通信     │  (Daemon)    │        │             │
└─────────┘              └──────────────┘        └─────────────┘
                              │
                              ▼
                         ┌─────────┐
                         │ SQLite  │
                         │ (任务存储)│
                         └─────────┘
```

**两个可执行文件，同一个代码库：**
- `bubbles` — CLI 工具，与 daemon 通信管理任务
- `bubblesd` — Daemon 常驻进程，负责调度和执行

## 技术选型

| 组件 | 选择 | 理由 |
|------|------|------|
| CLI 框架 | cobra | Go 生态最成熟的 CLI 框架 |
| 调度库 | robfig/cron/v3 | 标准 cron 表达式支持 |
| IPC | Unix Socket + JSON-RPC | 本地通信，无需端口 |
| 存储 | SQLite (mattn/go-sqlite3) | 轻量、可靠、单文件 |
| 进程管理 | fork + pid文件 | 标准守护进程模式 |
| 日志 | slog (Go 1.21+ 标准库) | 零依赖 |

## 项目结构

```
bubbles/
├── cmd/
│   ├── bubbles/          # CLI 入口
│   │   └── main.go
│   └── bubblesd/         # Daemon 入口
│       └── main.go
├── internal/
│   ├── cli/              # CLI 子命令
│   │   ├── root.go
│   │   ├── create.go     # 创建任务
│   │   ├── list.go       # 列出任务
│   │   ├── delete.go     # 删除任务
│   │   ├── log.go        # 查看执行日志
│   │   ├── daemon.go     # start/stop/status daemon
│   │   └── run.go        # 立即执行一次任务
│   ├── daemon/            # Daemon 核心
│   │   ├── daemon.go     # 启动/停止守护进程
│   │   ├── scheduler.go  # cron 调度器
│   │   └── executor.go   # 调用 claude code 执行任务
│   ├── ipc/               # CLI↔Daemon 通信
│   │   ├── client.go     # CLI 端客户端
│   │   ├── server.go     # Daemon 端服务
│   │   └── protocol.go   # 请求/响应定义
│   ├── store/             # 数据存储
│   │   ├── store.go      # SQLite 存储接口
│   │   └── migrations.go # 建表迁移
│   └── model/             # 数据模型
│       └── task.go        # Task, ExecutionLog 等
├── docs/
│   └── specs/             # 架构设计文档
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## 核心数据模型

```go
type Task struct {
    ID          string    // UUID
    Name        string    // 任务名称
    Prompt      string    // 发送给 claude code 的 prompt
    Schedule    string    // cron 表达式，空=一次性任务
    RunAt       time.Time // 一次性任务的指定执行时间
    WorkDir     string    // 工作目录
    Status      string    // active / paused / done
    CreatedAt   time.Time
    NextRunAt   time.Time // 下次执行时间
    LastRunAt   time.Time // 上次执行时间
}

type ExecutionLog struct {
    ID        string    // UUID
    TaskID    string
    Output    string    // claude code 输出
    Status    string    // success / failed / running
    StartedAt time.Time
    EndedAt   time.Time
}
```

## IPC 协议 (JSON-RPC over Unix Socket)

```go
type Request struct {
    Method  string      `json:"method"`   // "task.create", "task.list", ...
    Params  interface{} `json:"params"`
}

type Response struct {
    Result interface{} `json:"result,omitempty"`
    Error  string      `json:"error,omitempty"`
}
```

方法列表：
- `task.create` — 创建任务
- `task.list` — 列出任务
- `task.get` — 获取任务详情
- `task.delete` — 删除任务
- `task.pause` / `task.resume` — 暂停/恢复
- `task.run` — 立即执行
- `task.logs` — 获取执行日志
- `daemon.status` — 获取 daemon 状态

## CLI 命令设计

```bash
# Daemon 管理
bubbles daemon start          # 启动守护进程（后台）
bubbles daemon stop           # 停止守护进程
bubbles daemon status         # 查看状态

# 任务管理
bubbles create --name "每日总结" --schedule "0 9 * * *" --prompt "总结今天的代码变更" --dir /path/to/project
bubbles create --name "一次性检查" --at "2026-06-13T20:00:00" --prompt "运行代码审查" --dir /path/to/project
bubbles list                  # 列出所有任务
bubbles delete <task-id>      # 删除任务
bubbles pause <task-id>       # 暂停
bubbles resume <task-id>      # 恢复
bubbles run <task-id>         # 立即执行一次

# 日志
bubbles logs <task-id>        # 查看执行日志
bubbles logs <task-id> --last # 查看最近一次执行日志
```

## Daemon 核心流程

1. **启动**：读取 PID 文件检查是否已运行 → fork 后台进程 → 写 PID 文件 → 启动 Unix Socket 监听 → 加载 SQLite 中的活跃任务 → 注册 cron 调度 → 阻塞等待
2. **调度**：cron 触发 → 检查任务状态 → 调用 `claude` CLI 执行 → 记录 ExecutionLog → 更新 Task 的 LastRunAt/NextRunAt
3. **一次性任务**：到达指定时间后执行，执行完毕后状态改为 `done`
4. **优雅关闭**：SIGTERM/SIGINT → 停止接受新连接 → 等待运行中的任务完成 → 停止 cron → 关闭 DB → 删除 PID 文件

## 执行 Claude Code

```go
cmd := exec.Command("claude", "--print", "--output-format", "json", task.Prompt)
cmd.Dir = task.WorkDir
output, err := cmd.CombinedOutput()
```

使用 `claude --print` 非交互模式，`--output-format json` 获取结构化输出。

## 验证方式

```bash
make build
./bubbles daemon start
./bubbles create --name "test" --schedule "*/1 * * * *" --prompt "echo hello" --dir /tmp
./bubbles list
# 等待一分钟
./bubbles logs <task-id> --last
./bubbles delete <task-id>
./bubbles daemon stop
```