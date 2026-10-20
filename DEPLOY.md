# Bubbles 部署指南（新机器，git clone 起步）

> 目标：一台新 Mac/Linux 本地机，从 `git clone` 到完整运行投资决策系统，**无需任何历史数据**。
> 系统完全自包含：`invest-system/` 技能包随仓库分发，**数据文档（policy.yaml / decision-log.md）随包在 git 中，运行产出也落在包目录**（`INVEST_ROOT` 可覆盖到别处）。

## 0. 前置依赖

| 依赖 | 版本 | 用途 | 安装 |
|---|---|---|---|
| Xcode CLT / gcc | — | CGO 编译 SQLite | `xcode-select --install`（macOS） |
| Go | ≥ 1.26 | 构建 bubbles | https://go.dev/dl/ |
| git | — | 拉代码 | CLT 自带 |
| pi | 最新 | agent 运行时 | `curl -fsSL https://pi.dev/install.sh \| sh` |
| （可选）Node.js | ≥ 22.19 | npm 方式装 pi 时才需要 | — |

> `go.mod` 依赖全部走公共代理（无本地 replace），直接 `go mod download` 即可。

## 1. 一键部署

```bash
git clone <repo-url> bubbles && cd bubbles
./deploy.sh
```

`deploy.sh` 依次执行：依赖检查 → `make service`（构建 + 装 `/usr/local/bin` + 交给 systemd 托管启动 bubblesd；无 systemd 时回退 `make install` nohup 启动）→ pi 检查 → `invest-system/install.sh`（skills/agents/prompts + subagent 扩展）→ 提示补配置。

> **daemon 托管**：推荐 `make service`，systemd 会在 bubblesd 崩溃/被杀/OOM 后自动拉起（`Restart=on-failure`）。
> 管理用 `systemctl {status,stop,restart} bubblesd`；`bubbles daemon stop` 同样是优雅退出，不会被拉起。
> 升级二进制用 `make install`（检测到 systemd 单元时会自动 `systemctl restart`）。

## 2. 手动步骤（脚本跑完后要做的事）

### 2.1 bubbles 配置 `~/.bubbles/config.yaml`
```yaml
feishu_chat_id: oc_xxxx          # 目标群/会话 ID
feishu_drive_folder: fldxxxx     # 云文档目标文件夹 token（bubbles upload 用，文件夹 URL 末段）
work_dir: /path/to/bubbles       # 仓库路径
agent: pi                        # 或 claude
pi_path: pi
pi_model: mimo-v2.6-pro
```
飞书凭据走环境变量（写入 `~/.zshrc` 或 `launchctl setenv`）：
```bash
export FEISHU_APP_ID=cli_xxx
export FEISHU_APP_SECRET=xxx
```
飞书开放平台侧：应用需开通**机器人**能力 + `im.message.receive_v1` 事件订阅（长连接模式）+ `im:message:send_as_bot` 等消息权限 + **云文档权限**（`bubbles upload/archive` 文档同步用）：
- 创建文件夹：`space:folder:create`
- 上传文件：`drive:file:upload`
- 删除文件（替换旧版用）：`drive:file:delete`

或直接开总权限 `drive:drive`（一并覆盖以上三项）。授权入口：应用后台「权限管理」搜索 scope 开通（需应用管理员/或按提示链接一键申请）。

**文档可见性（重要）**：应用身份（tenant）上传的文件在“应用空间”，用户在自己的云文档看不到。要让文档出现在**你的「我的空间」**，需以用户身份上传：
1. 开放平台「安全设置 → 重定向 URL」登记 `http://localhost:18989/callback`
2. `bubbles login` 浏览器授权一次（token 自动续期，存 `~/.bubbles/feishu-user-token.json`）
3. `bubbles upload <文件> --user` → 文档直接进你的云文档（未指定文件夹时自动创建「我的空间/投资文档」）

#### 新环境权限一次性清单（实测验证过的权威 scope 名）

| 步骤 | 位置 | 内容 |
|---|---|---|
| 1 | 开放平台 → 权限管理 | 搜索并开通：`drive:file:upload`、`space:folder:create`、`space:document:delete`、`space:document:retrieve`、`docs:document:import`（或直接开总权限 `drive:drive`）；⚠ 同时勾选**用户身份 + 应用身份**两种授权类型；提示需发版就创建版本发布 |
| 2 | 开放平台 → 安全设置 | 重定向 URL 登记 `http://localhost:18989/callback` |
| 3 | 开放平台 → 事件订阅 | 长连接模式 + `im.message.receive_v1`；消息权限 `im:message:send_as_bot` |
| 4 | 本机 | `bubbles login`（授权页勾选全部权限）→ `bubbles upload xxx --user` |

**常见报错对照（踩坑记录）**：
| 报错 | 含义 | 解法 |
|---|---|---|
| 授权页 `error 20043` | scope 名不存在（如 `drive:file:delete` 是错的） | 用上表正确名（删除是 `space:document:delete`） |
| 授权页 “Missing permissions” | 应用未开通该权限（用户身份） | 去权限管理开通表中 scope，勾选用户身份 |
| API `99991679` | 已登录 token 缺该 scope | `bubbles login` 重新授权（scope 全集） |
| API `99991672` | 应用身份缺权限 | 去权限管理开对应 scope（应用身份） |
| 用户看不到文件 | 用的应用身份上传（文件在应用空间） | 加 `--user` 重新同步（状态按目标隔离，不重复） |

### 2.2 pi 侧装配（deploy.sh 已跑，换机器重跑幂等）
```bash
pi install npm:pi-web-access        # 联网搜索/抓取/PDF/视频
cd invest-system && ./install.sh    # 13 skills + 5 agents + 2 prompts + subagent 扩展
```

### 2.3 知识库（全新构建，自动初始化）
数据根默认就是 `invest-system/` 目录（git 管理），`install.sh` 初始化产出子目录：
```
policy.yaml      # 投资政策（risk_check 机器校验）
decision-log.md  # 决策日志
stocks/ holding/daily/ industry/ events/ review/ rss-digest/ _snapshots/ mx_data/output/
```
换目录：`export INVEST_ROOT=/your/path` 后重跑 `invest-system/install.sh`。
（可选）已有历史数据时，把旧文件拷入对应子目录即可，系统不依赖它们存在。

### 2.4 持仓主数据（SQLite，`~/.bubbles/bubbles.db` holdings 表）
全新环境为空，用 CLI 录入：
```bash
bubbles holding set HK.01810 --name 小米集团 --market 港股 --qty 15800 --cost 31.335 --state holding
```

### 2.5 定时流水线
```bash
cd invest-system && ./create-tasks.sh            # 5 条流水线
./create-tasks.sh --dry-run                      # 先预览
bubbles list                                     # 确认
```

### 2.6 数据密钥（可选，缺失自动降级腾讯行情+东财搜索）
```bash
export MX_APIKEY=xxx                    # 东方财富妙想数据
open -a OpenD                           # 富途港美股行情（127.0.0.1:11111）
echo '{"keys":[...]}' > ~/.web_search_keys.json   # Tavily（可选）
```

## 3. 验证清单

| # | 检查 | 期望 |
|---|---|---|
| 1 | `bubbles daemon status` | running ✓ |
| 2 | `bubbles holding list` | 迁移后的持仓 ✓ |
| 3 | `pi --print -p "列出你系统提示里所有 skill 的 name"` | 13 个 skill |
| 4 | 飞书发 `/help`、`/holding` | 返回命令帮助/持仓表 |
| 5 | `bubbles create -n 测试 -a "$(date -v+1M +%Y-%m-%dT%H:%M:%S)" -p "只回复 ok" && bubbles logs <id>` | 任务跑通 |
| 6 | `python3 invest-system/skills/holding-decision-review/scripts/risk_check.py <决策书> --stock-type value` | 门槛可用 |
| 7 | pi 里 `/持仓决策`（或 `/新股决策`） | subagent 三角流程跑通 |
| 8 | `bubbles upload invest-system --dry-run` 后真实跑一次；`bubbles archive invest-system --dry-run` | 文档出现在飞书云文档；归档清单正常 |

## 4. 常见问题

| 症状 | 原因/解法 |
|---|---|
| `make build` 报 sqlite 相关错 | CGO 未开或缺 C 编译器 → 装 Xcode CLT；Makefile 已设 `CGO_ENABLED=1` |
| go 版本报错 | 需 ≥1.26.3（go.mod），`go version` 核对 |
| pi 里 skill 不出现 | `settings.json` 的 `skills` 路径是否指向 `invest-system/skills`（`./install.sh` 会修）；`/reload` 或重启 pi |
| subagent 工具不存在 | `~/.pi/agent/extensions/subagent/` 缺失 → 重跑 `invest-system/install.sh` |
| 飞书无响应 | 检查 FEISHU_APP_ID/SECRET 环境变量、事件订阅（长连接）、`~/.bubbles/bubblesd.log` |
| `bubbles` 连不上 daemon | `bubblesd` 未启动 → `systemctl start bubblesd`（或 `make service` / `bubbles daemon start`） |
| daemon 异常退出 | `journalctl -u bubblesd -b` + `~/.bubbles/bubblesd.log`（panic 栈也写进该文件）；systemd 托管下会自动拉起 |
| bear 模型切换 | `/login anthropic` 后取消 `agents/invest-bear.md` 里 `# model:` 注释（模型异构，防表演性对抗） |

## 5. 目录速查

| 内容 | 位置 |
|---|---|
| 代码/技能包 | `<repo>/invest-system/`（skills/prompts/agents/install.sh/create-tasks.sh） |
| 二进制 | `/usr/local/bin/{bubbles,bubblesd}` |
| daemon 数据（含持仓 DB） | `~/.bubbles/`（bubbles.db / config.yaml / bubblesd.log） |
| pi 装配 | `~/.pi/agent/{settings.json,skills,prompts,agents,extensions}` |
| 数据根 | `invest-system/`（policy.yaml / decision-log.md / _snapshots/ / stocks/ …；`INVEST_ROOT` 可覆盖） |
