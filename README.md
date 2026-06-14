# Bubbles

飞书与Claude Code的交互框架，支持定时任务调度、指定时间任务、任务完成通知等功能。

## 特性

- 🕐 Cron 定时任务和一次性延迟任务
- 🔄 Daemon 常驻进程，后台自动调度
- 📋 CLI 完整管理：创建、暂停、恢复、删除、立即执行
- 📜 每次执行的完整日志记录
- 🤖 飞书机器人：自然语言对话、流式卡片回复、会话上下文保持
- 📅 飞书交互式任务管理：卡片按钮操作、表单创建定时任务
- ✅ 任务完成自动推送飞书通知

## 安装

```bash
# 需要 Go 1.21+ 和 CGO（SQLite 依赖）
make install  # 安装到 /usr/local/bin/，并启动服务
```

前置条件：
- [Claude Code CLI](https://claude.ai/code) — `claude` 命令需在 PATH 中
- CGO 编译环境（SQLite 需要）

## 快速开始

### 1. 创建配置文件

```bash
mkdir -p ~/.bubbles
cat > ~/.bubbles/config.yaml << 'EOF'
work_dir: /path/to/your/project   # 必填：Claude 的工作目录
# claude_path: claude              # 可选：Claude CLI 路径，默认 "claude"
# data_dir: ~/.bubbles             # 可选：数据目录，默认 ~/.bubbles

# 可选：飞书机器人配置
# feishu_app_id: ""
# feishu_app_secret: ""
# feishu_chat_id: ""               # 任务完成通知的默认推送群
EOF
```

环境变量可覆盖配置文件：`FEISHU_APP_ID`、`FEISHU_APP_SECRET`、`CLAUDE_PATH`、`BUBBLES_DATA_DIR`。

### 2. 启动守护进程

```bash
bubbles daemon start    # 后台启动（自动 fork）
bubbles daemon status   # 查看运行状态
bubbles daemon stop     # 停止
```

### 3. 创建任务

```bash
# cron 定时任务 — 每天 9 点总结代码变更
bubbles create \
  --name "每日总结" \
  --schedule "0 9 * * *" \
  --prompt "总结今天的代码变更" \
  --dir /path/to/project

# 一次性任务 — 20 分钟后执行
bubbles create \
  --name "代码审查" \
  --at "2026-06-13T20:00:00" \
  --prompt "运行代码审查并给出建议"
```

### 4. 管理任务

```bash
bubbles list              # 列出所有任务
bubbles run <task-id>     # 立即执行一次
bubbles pause <task-id>   # 暂停
bubbles resume <task-id>  # 恢复
bubbles delete <task-id>  # 删除
bubbles logs <task-id>          # 查看所有执行日志
bubbles logs <task-id> --last   # 查看最近一次
```

## 飞书机器人

配置 `feishu_app_id` 和 `feishu_app_secret` 后，daemon 启动时自动连接飞书。用户可以在飞书中直接与 Claude 对话，支持流式卡片回复和会话上下文保持。

### 对话交互

在飞书中 @机器人 发送任意消息，Claude 会以流式卡片形式回复，实时显示思考过程和最终结果。同一聊天窗口内的连续消息会自动复用 Claude 会话（30 分钟无活动后过期）。

### 飞书命令

| 命令 | 说明 |
|------|------|
| `/cron` | 查看所有任务列表，带暂停/恢复/删除按钮 |
| `/cron-new` | 打开表单创建定时任务（选择频率、日期、时间） |
| `/new` | 重置当前会话，下次消息开启新 Claude 会话 |

### 任务完成通知

配置 `feishu_chat_id` 后，任务执行完成会自动推送卡片到指定群，包含执行状态、耗时和输出摘要。

## 架构

```
┌─────────┐  Unix Socket  ┌──────────────┐  exec  ┌─────────────┐
│ bubbles  │ ◄──────────► │  bubblesd    │ ─────► │ Claude Code │
│  (CLI)   │   JSON-RPC   │  (Daemon)    │        │             │
└─────────┘              └──────┬───────┘        └─────────────┘
                                │
                          ┌─────┴─────┐
                          ▼           ▼
                      ┌────────┐  ┌────────┐
                      │ SQLite │  │ 飞书 WS │
                      └────────┘  └────────┘
```

- **bubbles** — CLI 客户端，通过 Unix Socket（`~/.bubbles/bubblesd.sock`）发送 JSON-RPC 请求
- **bubblesd** — 后台 Daemon，负责任务调度、IPC 服务、飞书 WebSocket 连接
- **SQLite** — 存储任务定义和执行日志（`~/.bubbles/bubbles.db`，WAL 模式）

### 两种执行路径

1. **定时任务** — 通过 CLI 或飞书表单创建，由调度器在指定时间触发。同步执行 `claude --print`，输出写入 SQLite。
2. **飞书对话** — 消息直接走流式管道（`stream-json`），实时推送卡片更新，自动审批所有工具权限。

## Cron 表达式

标准 5 段格式：

```
┌───────────── 分钟 (0-59)
│ ┌───────────── 小时 (0-23)
│ │ ┌───────────── 日 (1-31)
│ │ │ ┌───────────── 月 (1-12)
│ │ │ │ ┌───────────── 星期 (0-6, 0=周日)
│ │ │ │ │
* * * * *
```

常用示例：
- `*/5 * * * *` — 每 5 分钟
- `0 9 * * *` — 每天 9 点
- `0 9 * * 1-5` — 工作日每天 9 点
- `0 0 1 * *` — 每月 1 号零点

## License

MIT
