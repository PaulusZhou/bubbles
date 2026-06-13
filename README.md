# Bubbles

定时调用 [Claude Code](https://claude.ai/code) 执行任务的命令行工具。

## 特性

- 🕐 支持 cron 表达式定时任务
- ⏰ 支持指定时间的一次性任务
- 🔄 Daemon 常驻进程，后台自动调度
- 📋 CLI 管理：创建、暂停、恢复、删除任务
- 📜 完整的执行日志记录

## 安装

```bash
make build
make install
```

## 使用

### 启动守护进程

```bash
bubbles daemon start
bubbles daemon status
bubbles daemon stop
```

### 创建任务

```bash
# cron 定时任务 — 每天 9 点总结代码变更
bubbles create \
  --name "每日总结" \
  --schedule "0 9 * * *" \
  --prompt "总结今天的代码变更" \
  --dir /path/to/project

# 一次性任务 — 指定时间执行代码审查
bubbles create \
  --name "代码审查" \
  --at "2026-06-13T20:00:00" \
  --prompt "运行代码审查" \
  --dir /path/to/project
```

### 管理任务

```bash
bubbles list              # 列出所有任务
bubbles run <task-id>     # 立即执行一次
bubbles pause <task-id>   # 暂停
bubbles resume <task-id>  # 恢复
bubbles delete <task-id>  # 删除
```

### 查看日志

```bash
bubbles logs <task-id>          # 查看所有执行日志
bubbles logs <task-id> --last   # 查看最近一次
```

## 架构

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

- **bubbles** — CLI 工具，通过 Unix Socket 与 daemon 通信
- **bubblesd** — Daemon 常驻进程，负责调度和执行
- **SQLite** — 存储任务和执行日志 (~/.bubbles/bubbles.db)

## Cron 表达式

使用标准 5 段 cron 表达式：

```
┌───────────── 分钟 (0-59)
│ ┌───────────── 小时 (0-23)
│ │ ┌───────────── 日 (1-31)
│ │ │ ┌───────────── 月 (1-12)
│ │ │ │ ┌───────────── 星期 (0-6, 0=周日)
│ │ │ │ │
* * * * *
```

示例：
- `*/5 * * * *` — 每 5 分钟
- `0 9 * * 1-5` — 工作日每天 9 点
- `0 0 1 * *` — 每月 1 号零点

## 依赖

- Go 1.21+
- CGO (SQLite 需要)
- Claude Code CLI (`claude` 命令需在 PATH 中)

## License

MIT
