# invest-system — 个人投资决策系统（pi 技能包）

决策驱动的投研体系独立技能包：**一个决策骨架、两个证据生产者、两个信号输入、一个复盘、一个特例**，加数据通道与 pi 角色 agents。与 `investment/`（知识库/产出目录）解耦：**本包只含流程定义，产出仍写入 `investment/`**。

## 安装

```bash
cd /Users/pauluszhou/Projects/bubbles/invest-system
./install.sh        # 装配 skill/prompt/agent
./create-tasks.sh   # 注册 5 条 bubbles 定时流水线（需 bubblesd）
```

安装内容（幂等，可重复执行）：
| 装配 | 目标位置 | 说明 |
|---|---|---|
| 13 个 skill | `~/.pi/agent/skills/`（软链） | 手动 + settings 指向本包 `skills/` |
| 2 个流程 prompt | `~/.pi/agent/prompts/`（软链） | `/持仓决策` `/新股决策` |
| 3 个 agent 定义 | `~/.pi/agent/agents/`（软链） | invest-analyst / invest-bear / invest-risk |
| subagent 扩展 | `~/.pi/agent/extensions/subagent/` | agent 运行引擎（缺省时自动装） |

卸载：删除 `~/.pi/agent/{skills,prompts,agents}` 下的本包软链，并把 `settings.json` 的 `skills` 项还原。

## 两大决策流程

### 1. `/持仓决策 [ticker]` — 手里的股票怎么办（假设兑现追踪）
```
触发(收盘/红信号/止损临近/复核日) → 持仓快照 → 环境与信号 → 假设兑现核对
→ 定向验证 → 空头证伪 + 风控 → 保持/加仓/卖出 → decision-record 落库 → 拍板 → 30天复核
```

### 2. `/新股决策 [标的|主题]` — 下一笔钱买什么（假设从无到有）
```
候选生成 → 估值初筛 → 深度尽调(P1/P2/P8/P9) → 假设对抗(analyst→bear→risk)
→ 组合适配(持仓数/行业/现金/替换谁) → 买入/否决 → 落库 → 拍板 → 转流程1
```

## Skill 清单（13）

| Skill | 角色 | 流程1 | 流程2 |
|---|---|:---:|:---:|
| holding-decision-review | **决策骨架**（假设-验证-就绪度门-decision-record） | 主干 | 主干 |
| stock-buying-evaluation | 个股尽调（P1/P2/P8/P9，不出决策） | — | 尽调 |
| industry-deep-analysis | 产业深度（价值链/龙头 → 候选生成） | — | 候选 |
| industry-monitor | 行业信号监测 | 输入 | 输入 |
| signal-monitor | **资讯+事件+会议 PEST**（rss/event/meeting 三合一，红黄绿分级） | 输入 | 输入 |
| stock-trade-review | 复盘 + 纪律裁判（30 天回填/否决错杀统计） | 回填 | 回填 |
| private-company-analysis | 未上市标的六维分析 | — | 条件 |
| futuapi / mx-data / mx-search | 数据通道（行情/财务/搜索，需 OpenD / MX_APIKEY） | 底座 | 底座 |
| multi-tavily-search / mx-moni / ima-skill | 工具备用（Tavily / 模拟盘 / ima 知识库） | 可选 | 可选 |

## Agent 角色（agents/，经 subagent 扩展运行于独立上下文）

| Agent | 职责 | 模型档位 | 输出 |
|---|---|---|---|
| invest-scout | 侦察员：数据快照核验/候选挖掘初筛/证据搜集（宽度优先，不判断） | 便宜快速 | 结构化情报 |
| invest-analyst | 假设-验证-决策主流程 | 旗舰 | 决策书草稿 + decision-record |
| invest-bear | 独立证伪（只拿结论不拿过程） | 旗舰 | 证伪报告 |
| invest-risk | policy.yaml 硬规则 + 就绪度门审计 | 中档 | 风控审查表（可否决） |
| invest-reviewer | 事后独立复盘：假设兑现 + 纪律评分回填 | 中档 | 复盘报告 + decision-log 回填 |

生命周期分工：**事前 scout（猎）→ 事中 analyst+bear+risk（判）→ 事后 reviewer（审）**；组合经理永远是人。

**多空分歧标【分歧】交人裁决；决策权在 Paulus，系统不自动交易。**

## 触发方式

| 入口 | 持仓决策 | 新股决策 |
|---|---|---|
| pi 手动 | `/持仓决策 [ticker]` | `/新股决策 [标的\|主题]` |
| 飞书对话 | 直接说“持仓决策 小米”（skill 自动路由） | 直接说“找几只机器人股票” |
| 定时 | 收盘检查（工作日 15:30，只跑触发检查）；周度全量（周六 09:00） | 周度决策会（周六 10:00） |
| 事件 | 红信号/止损预警/复核日到期 → 信号扫描（交易时段每小时）强制触发 | 催化事件/满仓替换 → 人工或月度审计触发 |
| 一次性 | `bubbles create --at ... -p "持仓决策 HK.01810"` | 同左 |

定时任务 prompt 点名 skill（`pi --print` 不展开斜杠命令），与手动共用同一套流程。

## 结构

```
invest-system/
├── install.sh                  # 一键装配（幂等）
├── create-tasks.sh             # 注册 5 条定时流水线（--dry-run 可预览）
├── skills/                     # 13 个 skill（详见上表）
│   └── holding-decision-review/references/
│       ├── decision-record.md  # 决策结果 schema
│       ├── templates.md        # 决策书模板
│       └── data-sources.md     # 数据通道与降级链
├── prompts/
│   ├── 持仓决策.md             # /持仓决策
│   └── 新股决策.md             # /新股决策
└── agents/
    ├── invest-scout.md         # 侦察员
    ├── invest-analyst.md
    ├── invest-bear.md
    ├── invest-risk.md
    └── invest-reviewer.md      # 复盘官/纪律裁判
```

## 文档归档与同步

**归档**：旧文档按月归档到 `_archive/YYYY-MM/`（保留目录结构，台账/脚本永不归档）：

```bash
bubbles archive invest-system --days 90        # 归档 90 天前的文档
bubbles archive invest-system --before 2026-07-01
bubbles archive invest-system --dry-run        # 预览
```
豁免：`policy.yaml`、`decision-log.md`（决策台账）、`_archive/`、隐藏文件、默认排除 `SKILL.md`/`scripts/`/`references/`（`--exclude` 可调）。

**同步到飞书（推荐：lark-cli，无需脚本）**：官方 CLI 已装（`npx @larksuite/cli@latest install`），agent/人直接一条命令传文档，URL 直接可用：

```bash
lark-cli markdown +create --file 决策书.md --as user      # → 返回 file_token + URL
lark-cli drive +upload --file 报告.xlsx --as user         # 非 md 文件
lark-cli auth login --domain drive,docs,markdown          # 换机/重装时设备码登录（发链接即授权）
```

决策书在流程中由 agent 顺手上传（见 prompts 第 6 步），飞书消息附文档链接。

**批量同步（备用）**：`bubbles upload invest-system --user [--docx]`——自带增量状态、目录镜像、md→docx 导入，适合全量归档；状态在 `~/.bubbles/feishu-drive-state.json`。

## 依赖

- `bubblesd`（决策落库 `bubbles holding`、定时流水线、飞书 `/holding`）
- 数据密钥（可选，缺失自动降级腾讯行情+东财搜索）：`MX_APIKEY`、FutuOpenD(127.0.0.1:11111)、`~/.web_search_keys.json`
- 知识库根 `INVEST_ROOT`（默认 `{INVEST_ROOT}`，`install.sh` 自动初始化：policy.yaml / decision-log.md / 目录树）

## 防决策失误机制（v2 加固）

| 风险 | 机制 | 实现 |
|---|---|---|
| 纪律参数未校准 | 波动率止损（2×ATR 与 -20% 取宽，硬底 -35%）+ **估值带按类型分类**（周期股禁 PE） | policy.yaml + risk_check |
| 模型同源偏差（表演性对抗） | bear **模型异构**（agent 内注释一键切换 claude）；scout/risk/reviewer 走 flash 档 | agents/*.md frontmatter |
| 就绪度门自评放水 | **risk_check.py 程序化硬门槛**（仓位算术/估值带/bear 反对/止损深度机械比对，退出码 1=阻断） | scripts/risk_check.py |
| 后视偏差 | **证据快照落盘** `_snapshots/` + snapshot_index 强制引用 | scout 模式 A + decision-record |
| 结果偏差（用 30 天盈亏评判 12 月假设） | 30 天只核对驱动因子，12 个月才评 outcome + **beta/alpha 归因** | decision-record + invest-reviewer |
| 表演性/叙事驱动 | bear **自行取证 ≥3 新源**（输入茧房打破）+ 证伪条件可触发性审核 + 看空目标价 | invest-bear |
| 过度交易 | **决策冷却期 7 天**（止损/证伪除外） | policy + SKILL |
| 极端行情踩踏 | **熔断**：大盘 -5%/组合 -8% → 冻新开仓 24h，卖出需 bear 二审+人工 | policy + SKILL |
| 伪分散 | **相关簇合计 ≤50%**（同一驱动逻辑合并计算） | policy + SKILL |
| 未验证就上真仓 | **沙盒模式**：买卖只落库标记，连续 2 次纪律 A 级后退出 | policy sandbox |
| 卖出/否决后不回访 | 卖出后 60 天回访 + 否决错杀统计 | invest-reviewer |
| 长上下文质量衰减 | 全量持仓**每标的独立子流程** | prompts + create-tasks |
| 流水线静默失败 | 月度审计心跳（查任务成功率）+ 通知失败重试 | create-tasks |
