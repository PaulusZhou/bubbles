# bubbles-pi 扩展

把 [pi](https://github.com/earendil-works/pi) 会话接入 bubblesd 的扩展：让 pi 在执行任务时可以主动给飞书发通知、创建/管理定时任务。

## 提供的能力

| 名称 | 类型 | 说明 |
|---|---|---|
| `feishu_notify` | 工具 | 通过 bubblesd 向飞书发送文本通知（`feishu.notify`） |
| `bubbles_task` | 工具 | 创建 / 查询 / 立即运行定时任务（`task.create` / `task.list` / `task.run`） |
| `bubbles_status` | 工具 | 查询 bubblesd 状态（`daemon.status`） |
| `/bubbles` | 命令 | 在会话里查看 daemon 状态 |

## 安装

```bash
cp -r extensions/bubbles-pi ~/.pi/agent/extensions/
```

或放进具体项目的 `.pi/extensions/`。pi 通过 jiti 直接加载 TypeScript，无需编译。

## 前提

- bubblesd 正在运行，且已配置飞书（`feishu_notify` 需要）。
- IPC socket 默认在 `~/.bubbles/bubblesd.sock`；如果 `bubbles` 使用自定义数据目录，需给 pi 进程设置 `BUBBLES_DATA_DIR` 环境变量（bubblesd 的 `work_dir` 会作为 pi 的工作目录，扩展随 pi 一起继承 daemon 的环境）。

## 使用示例

在飞书对话或 pi 终端里：

> 帮我调研一下竞品 X 的定价，完成后用 feishu_notify 告诉我结果要点；
> 再创建一个每天早上 9 点生成日报的任务。

pi 会在执行中调用 `feishu_notify` 推送进度、用 `bubbles_task` 注册日报任务。

## 安全提示

- **审批门**：扩展会对危险工具调用发起确认（显式声明 `destructiveHint` 的工具，以及匹配危险模式的 bash 命令，如 `rm -rf`、`sudo`、`git push --force` 等）。在 bubblesd 的 RPC 通道下，确认会变成**飞书卡片按钮**（✅允许 / ❌拒绝）；在交互终端下是原生对话框；定时任务等无 UI 场景保持自动执行。
- `bubbles_task` 标记为 `destructiveHint: true`，每次调用都会要求确认。
- 扩展与 pi 进程同权限运行，仅加载可信来源的扩展。

## 会话分支（重新回答）

飞书对话中：

- 每条最终回复卡片带 **🔄 重新回答** 按钮：回到当前提问重新生成回答（原回答保留在分支历史中）
- `/refork` 命令：列出历史提问，选择任意一个从那里重新生成

实现基于 pi RPC 的 `fork` / `get_fork_messages`，由 bubblesd 的卡片按钮路由。

## 交互对照表

| pi 扩展 UI | 飞书侧表现 |
|---|---|
| `ctx.ui.confirm` | 卡片按钮 ✅允许 / ❌拒绝 |
| `ctx.ui.select` | 每个选项一个按钮 |
| `ctx.ui.input` / `editor` | 自动取消（卡片暂不支持文本回传） |
| `ctx.ui.notify` | 聊天文本消息 |
| `setStatus` / `setWidget` 等 | 忽略 |

## 尚未覆盖 / 后续工作

- `ctx.ui.input` / `editor` 的自由文本输入（可用飞书卡片表单组件实现）
- 分支树导航（切换回旧分支的回答）
